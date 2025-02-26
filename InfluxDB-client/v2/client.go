package influxdb_client

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	stscache "github.com/grafana/grafana/InfluxDB-client/memcache"
	"github.com/grafana/grafana/InfluxDB-client/models"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ContentEncoding string

var c, err = NewHTTPClient(HTTPConfig{
	Addr: "http://192.168.1.103:8086",
})

var stscacheConn = stscache.New("192.168.1.102:11211")

var TagKV MeasurementTagMap
var Fields map[string]map[string]string

var QueryTemplateToPartialSegment = make(map[string]string)
var SegmentToMetric = make(map[string]string)

var QueryTemplates = make(map[string]string)
var SegmentToFields = make(map[string]string)
var SeprateSegments = make(map[string][]string)

var UseCache = "db"

var MaxThreadNum = 64

const STRINGBYTELENGTH = 32

var (
	DB    = "iot_small"
	IOTDB = "iot"
)
var DbName = ""

var STsCacheURL string

const (
	DefaultEncoding ContentEncoding = ""
	GzipEncoding    ContentEncoding = "gzip"
)

type HTTPConfig struct {
	Addr string

	Username string

	Password string

	UserAgent string

	Timeout time.Duration

	InsecureSkipVerify bool

	TLSConfig *tls.Config

	Proxy func(req *http.Request) (*url.URL, error)

	WriteEncoding ContentEncoding
}

type BatchPointsConfig struct {
	Precision string

	Database string

	RetentionPolicy string

	WriteConsistency string
}

type Client interface {
	Ping(timeout time.Duration) (time.Duration, string, error)

	Write(bp BatchPoints) error

	Query(q Query) (*Response, error)

	QueryFromDatabase(query Query) (int64, *Response, error)

	QueryAsChunk(q Query) (*ChunkedResponse, error)

	Close() error
}

func NewHTTPClient(conf HTTPConfig) (Client, error) {
	if conf.UserAgent == "" {
		conf.UserAgent = "InfluxDBClient"
	}

	u, err := url.Parse(conf.Addr)
	if err != nil {
		return nil, err
	} else if u.Scheme != "http" && u.Scheme != "https" {
		m := fmt.Sprintf("Unsupported protocol scheme: %s, your address"+
			" must start with http:// or https://", u.Scheme)
		return nil, errors.New(m)
	}

	switch conf.WriteEncoding {
	case DefaultEncoding, GzipEncoding:
	default:
		return nil, fmt.Errorf("unsupported encoding %s", conf.WriteEncoding)
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: conf.InsecureSkipVerify,
		},
		Proxy: conf.Proxy,
	}
	if conf.TLSConfig != nil {
		tr.TLSClientConfig = conf.TLSConfig
	}
	return &client{
		url:       *u,
		username:  conf.Username,
		password:  conf.Password,
		useragent: conf.UserAgent,
		httpClient: &http.Client{
			Timeout:   conf.Timeout,
			Transport: tr,
		},
		transport: tr,
		encoding:  conf.WriteEncoding,
	}, nil
}

func (c *client) Ping(timeout time.Duration) (time.Duration, string, error) {
	now := time.Now()

	u := c.url
	u.Path = path.Join(u.Path, "ping")

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return 0, "", err
	}

	req.Header.Set("User-Agent", c.useragent)

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	if timeout > 0 {
		params := req.URL.Query()
		params.Set("wait_for_leader", fmt.Sprintf("%.0fs", timeout.Seconds()))
		req.URL.RawQuery = params.Encode()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}

	if resp.StatusCode != http.StatusNoContent {
		var err = errors.New(string(body))
		return 0, "", err
	}

	version := resp.Header.Get("X-Influxdb-Version")
	return time.Since(now), version, nil
}

func (c *client) Close() error {
	c.transport.CloseIdleConnections()
	return nil
}

type client struct {
	url        url.URL
	username   string
	password   string
	useragent  string
	httpClient *http.Client
	transport  *http.Transport
	encoding   ContentEncoding
}

type BatchPoints interface {
	AddPoint(p *Point)
	AddPoints(ps []*Point)
	Points() []*Point
	Precision() string
	SetPrecision(s string) error
	Database() string
	SetDatabase(s string)
	WriteConsistency() string
	SetWriteConsistency(s string)
	RetentionPolicy() string
	SetRetentionPolicy(s string)
}

func NewBatchPoints(conf BatchPointsConfig) (BatchPoints, error) {
	if conf.Precision == "" {
		conf.Precision = "ns"
	}
	if _, err := time.ParseDuration("1" + conf.Precision); err != nil {
		return nil, err
	}
	bp := &batchpoints{
		database:         conf.Database,
		precision:        conf.Precision,
		retentionPolicy:  conf.RetentionPolicy,
		writeConsistency: conf.WriteConsistency,
	}
	return bp, nil
}

type batchpoints struct {
	points           []*Point
	database         string
	precision        string
	retentionPolicy  string
	writeConsistency string
}

func (bp *batchpoints) AddPoint(p *Point) {
	bp.points = append(bp.points, p)
}

func (bp *batchpoints) AddPoints(ps []*Point) {
	bp.points = append(bp.points, ps...)
}

func (bp *batchpoints) Points() []*Point {
	return bp.points
}

func (bp *batchpoints) Precision() string {
	return bp.precision
}

func (bp *batchpoints) Database() string {
	return bp.database
}

func (bp *batchpoints) WriteConsistency() string {
	return bp.writeConsistency
}

func (bp *batchpoints) RetentionPolicy() string {
	return bp.retentionPolicy
}

func (bp *batchpoints) SetPrecision(p string) error {
	if _, err := time.ParseDuration("1" + p); err != nil {
		return err
	}
	bp.precision = p
	return nil
}

func (bp *batchpoints) SetDatabase(db string) {
	bp.database = db
}

func (bp *batchpoints) SetWriteConsistency(wc string) {
	bp.writeConsistency = wc
}

func (bp *batchpoints) SetRetentionPolicy(rp string) {
	bp.retentionPolicy = rp
}

type Point struct {
	pt models.Point
}

func NewPoint(
	name string,
	tags map[string]string,
	fields map[string]interface{},
	t ...time.Time,
) (*Point, error) {
	var T time.Time
	if len(t) > 0 {
		T = t[0]
	}

	pt, err := models.NewPoint(name, models.NewTags(tags), fields, T)
	if err != nil {
		return nil, err
	}
	return &Point{
		pt: pt,
	}, nil
}

func (p *Point) String() string {
	return p.pt.String()
}

func (p *Point) PrecisionString(precision string) string {
	return p.pt.PrecisionString(precision)
}

func (p *Point) Name() string {
	return string(p.pt.Name())
}

func (p *Point) Tags() map[string]string {
	return p.pt.Tags().Map()
}

func (p *Point) Time() time.Time {
	return p.pt.Time()
}

func (p *Point) UnixNano() int64 {
	return p.pt.UnixNano()
}

func (p *Point) Fields() (map[string]interface{}, error) {
	return p.pt.Fields()
}

func NewPointFrom(pt models.Point) *Point {
	return &Point{pt: pt}
}

func (c *client) Write(bp BatchPoints) error {
	var b bytes.Buffer

	var w io.Writer
	if c.encoding == GzipEncoding {
		w = gzip.NewWriter(&b)
	} else {
		w = &b
	}

	for _, p := range bp.Points() {
		if p == nil {
			continue
		}
		if _, err := io.WriteString(w, p.pt.PrecisionString(bp.Precision())); err != nil {
			return err
		}

		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}

	if c, ok := w.(io.Closer); ok {
		if err := c.Close(); err != nil {
			return err
		}
	}

	u := c.url
	u.Path = path.Join(u.Path, "write")

	req, err := http.NewRequest("POST", u.String(), &b)
	if err != nil {
		return err
	}
	if c.encoding != DefaultEncoding {
		req.Header.Set("Content-Encoding", string(c.encoding))
	}
	req.Header.Set("Content-Type", "")
	req.Header.Set("User-Agent", c.useragent)
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	params := req.URL.Query()
	params.Set("db", bp.Database())
	params.Set("rp", bp.RetentionPolicy())
	params.Set("precision", bp.Precision())
	params.Set("consistency", bp.WriteConsistency())
	req.URL.RawQuery = params.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		var err = errors.New(string(body))
		return err
	}

	return nil
}

type Query struct {
	Command         string
	Database        string
	RetentionPolicy string
	Precision       string
	Chunked         bool
	ChunkSize       int
	Parameters      map[string]interface{}
}

type Params map[string]interface{}

func NewQuery(command, database, precision string) Query {
	return Query{
		Command:    command,
		Database:   database,
		Precision:  precision, // autogen
		Parameters: make(map[string]interface{}),
	}
}

func NewQueryWithRP(command, database, retentionPolicy, precision string) Query {
	return Query{
		Command:         command,
		Database:        database,
		RetentionPolicy: retentionPolicy,
		Precision:       precision,
		Parameters:      make(map[string]interface{}),
	}
}

func NewQueryWithParameters(command, database, precision string, parameters map[string]interface{}) Query {
	return Query{
		Command:    command,
		Database:   database,
		Precision:  precision,
		Parameters: parameters,
	}
}

type Response struct {
	Results []Result
	Err     string `json:"error,omitempty"`
}

func (r *Response) Error() error {
	if r.Err != "" {
		return errors.New(r.Err)
	}
	for _, result := range r.Results {
		if result.Err != "" {
			return errors.New(result.Err)
		}
	}
	return nil
}

type Message struct {
	Level string
	Text  string
}

type Result struct {
	StatementId int `json:"statement_id"`
	Series      []models.Row
	Messages    []*Message
	Err         string `json:"error,omitempty"`
}

func (c *client) QueryFromDatabase(q Query) (int64, *Response, error) {
	var length int64
	req, err := c.createDefaultRequest(q)
	if err != nil {
		return 0, nil, err
	}
	params := req.URL.Query()
	if q.Chunked {
		params.Set("chunked", "true")
		if q.ChunkSize > 0 {
			params.Set("chunk_size", strconv.Itoa(q.ChunkSize))
		}
		req.URL.RawQuery = params.Encode()
	}
	resp, err := c.httpClient.Do(req)
	length = resp.ContentLength
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	if err := checkResponse(resp); err != nil {
		return 0, nil, err
	}

	var response Response
	if q.Chunked {
		cr := NewChunkedResponse(resp.Body)
		for {
			r, err := cr.NextResponse()
			if err != nil {
				if err == io.EOF {
					break
				}
				return 0, nil, err
			}

			if r == nil {
				break
			}

			response.Results = append(response.Results, r.Results...)
			if r.Err != "" {
				response.Err = r.Err
				break
			}
		}
	} else {
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		decErr := dec.Decode(&response)

		if decErr != nil && decErr.Error() == "EOF" && resp.StatusCode != http.StatusOK {
			decErr = nil
		}
		if decErr != nil {
			return 0, nil, fmt.Errorf("unable to decode json: received status code %d err: %s", resp.StatusCode, decErr)
		}
	}

	if resp.StatusCode != http.StatusOK && response.Error() == nil {
		return 0, &response, fmt.Errorf("received status code %d from server", resp.StatusCode)
	}
	return length, &response, nil
}

func (c *client) Query(q Query) (*Response, error) {
	req, err := c.createDefaultRequest(q)
	if err != nil {
		return nil, err
	}
	params := req.URL.Query()
	if q.Chunked {
		params.Set("chunked", "true")
		if q.ChunkSize > 0 {
			params.Set("chunk_size", strconv.Itoa(q.ChunkSize))
		}
		req.URL.RawQuery = params.Encode()
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	var response Response
	if q.Chunked {
		cr := NewChunkedResponse(resp.Body)
		for {
			r, err := cr.NextResponse()
			if err != nil {
				if err == io.EOF {
					break
				}
				return nil, err
			}

			if r == nil {
				break
			}

			response.Results = append(response.Results, r.Results...)
			if r.Err != "" {
				response.Err = r.Err
				break
			}
		}
	} else {
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		decErr := dec.Decode(&response)

		if decErr != nil && decErr.Error() == "EOF" && resp.StatusCode != http.StatusOK {
			decErr = nil
		}
		if decErr != nil {
			return nil, fmt.Errorf("unable to decode json: received status code %d err: %s", resp.StatusCode, decErr)
		}
	}

	if resp.StatusCode != http.StatusOK && response.Error() == nil {
		return &response, fmt.Errorf("received status code %d from server", resp.StatusCode)
	}
	return &response, nil
}

func (c *client) QueryAsChunk(q Query) (*ChunkedResponse, error) {
	req, err := c.createDefaultRequest(q)
	if err != nil {
		return nil, err
	}
	params := req.URL.Query()
	params.Set("chunked", "true")
	if q.ChunkSize > 0 {
		params.Set("chunk_size", strconv.Itoa(q.ChunkSize))
	}
	req.URL.RawQuery = params.Encode()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return NewChunkedResponse(resp.Body), nil
}

func checkResponse(resp *http.Response) error {
	if resp.Header.Get("X-Influxdb-Version") == "" && resp.StatusCode >= http.StatusInternalServerError {
		body, err := io.ReadAll(resp.Body)
		if err != nil || len(body) == 0 {
			return fmt.Errorf("received status code %d from downstream server", resp.StatusCode)
		}

		return fmt.Errorf("received status code %d from downstream server, with response body: %q", resp.StatusCode, body)
	}

	if cType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); cType != "application/json" {
		body, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1024))
		if err != nil || len(body) == 0 {
			return fmt.Errorf("expected json response, got empty body, with status: %v", resp.StatusCode)
		}

		return fmt.Errorf("expected json response, got %q, with status: %v and response body: %q", cType, resp.StatusCode, body)
	}
	return nil
}

func (c *client) createDefaultRequest(q Query) (*http.Request, error) {
	u := c.url
	u.Path = path.Join(u.Path, "query")

	jsonParameters, err := json.Marshal(q.Parameters)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "")
	req.Header.Set("User-Agent", c.useragent)

	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	params := req.URL.Query()
	params.Set("q", q.Command)
	params.Set("db", q.Database)
	if q.RetentionPolicy != "" {
		params.Set("rp", q.RetentionPolicy)
	}
	params.Set("params", string(jsonParameters))

	if q.Precision != "" {
		params.Set("epoch", q.Precision)
	}
	req.URL.RawQuery = params.Encode()

	return req, nil

}

type duplexReader struct {
	r io.ReadCloser
	w io.Writer
}

func (r *duplexReader) Read(p []byte) (n int, err error) {
	n, err = r.r.Read(p)
	if err == nil {
		r.w.Write(p[:n])
	}
	return n, err
}

func (r *duplexReader) Close() error {
	return r.r.Close()
}

type ChunkedResponse struct {
	dec    *json.Decoder
	duplex *duplexReader
	buf    bytes.Buffer
}

func NewChunkedResponse(r io.Reader) *ChunkedResponse {
	rc, ok := r.(io.ReadCloser)
	if !ok {
		rc = ioutil.NopCloser(r)
	}
	resp := &ChunkedResponse{}
	resp.duplex = &duplexReader{r: rc, w: &resp.buf}
	resp.dec = json.NewDecoder(resp.duplex)
	resp.dec.UseNumber()
	return resp
}

func (r *ChunkedResponse) NextResponse() (*Response, error) {
	var response Response
	if err := r.dec.Decode(&response); err != nil {
		if err == io.EOF {
			return nil, err
		}
		io.Copy(ioutil.Discard, r.duplex)
		return nil, errors.New(strings.TrimSpace(r.buf.String()))
	}

	r.buf.Reset()
	return &response, nil
}

func (r *ChunkedResponse) Close() error {
	return r.duplex.Close()
}

var CacheHash = make(map[string]int)

func GetCacheHashValue(fields string) int {
	CacheNum := len(STsConnArr)

	if CacheNum == 0 {
		CacheNum = 1
	}
	if _, ok := CacheHash[fields]; !ok {
		value := len(CacheHash) % CacheNum
		CacheHash[fields] = value
	}
	hashValue := CacheHash[fields]
	return hashValue
}

var mtx sync.Mutex

var STsConnArr []*stscache.Client

func InitStsConns() []*stscache.Client {
	conns := make([]*stscache.Client, 0)
	for i := 0; i < MaxThreadNum; i++ {
		conns = append(conns, stscache.New(STsCacheURL))
	}
	return conns
}

func InitStsConnsArr(urlArr []string) []*stscache.Client {
	conns := make([]*stscache.Client, 0)
	for i := 0; i < len(urlArr); i++ {
		conns = append(conns, stscache.New(urlArr[i]))
	}
	return conns
}

var num = 0

func STsCacheClient(conn Client, queryString string) (*Response, uint64, uint8) {

	CacheNum := len(STsConnArr)

	if CacheNum == 0 {
		CacheNum = 1
	}

	byteLength := uint64(0)
	hitKind := uint8(0)

	queryTemplate, startTime, endTime, tags := GetQueryTemplate(queryString)

	mtx.Lock()

	partialSegment := ""
	fields := ""
	metric := ""
	if ps, ok := QueryTemplateToPartialSegment[queryTemplate]; !ok {
		partialSegment, fields, metric = GetPartialSegmentAndFields(queryString)
		QueryTemplateToPartialSegment[queryTemplate] = partialSegment
		SegmentToFields[partialSegment] = fields
		SegmentToMetric[partialSegment] = metric
	} else {
		partialSegment = ps
		fields = SegmentToFields[partialSegment]
		metric = SegmentToMetric[partialSegment]
	}

	semanticSegment := GetTotalSegment(metric, tags, partialSegment)
	starSegment := GetStarSegment(metric, partialSegment)

	CacheIndex := GetCacheHashValue(fields)
	fields = "time[int64]," + fields
	datatypes := GetDataTypeArrayFromSF(fields)

	mtx.Unlock()

	values, _, err := STsConnArr[CacheIndex].Get(semanticSegment, startTime, endTime)
	if err != nil {
		q := NewQuery(queryString, DB, "s")
		resp, err := conn.Query(q)
		if err != nil {
			log.Println(queryString)
		}

		if !ResponseIsEmpty(resp) {
			numOfTab := GetNumOfTable(resp)
			remainValues := ResponseToByteArrayWithParams(resp, datatypes, tags, metric, partialSegment)
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: remainValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		} else {
			singleSemanticSegment := GetSingleSegment(metric, partialSegment, tags)
			emptyValues := make([]byte, 0)
			for _, ss := range singleSemanticSegment {
				zero, _ := Int64ToByteArray(int64(0))
				emptyValues = append(emptyValues, []byte(ss)...)
				emptyValues = append(emptyValues, []byte(" ")...)
				emptyValues = append(emptyValues, zero...)
			}

			numOfTab := int64(len(singleSemanticSegment))
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		}

		return resp, byteLength, hitKind

	} else {
		convertedResponse, flagNum, flagArr, timeRangeArr, tagArr := ByteArrayToResponseWithDatatype(values, datatypes)

		if flagNum == 0 {
			hitKind = 2
			return convertedResponse, byteLength, hitKind
		} else {
			hitKind = 1

			remainQueryString, minTime, maxTime := RemainQueryString(queryString, queryTemplate, flagArr, timeRangeArr, tagArr)
			remainTags := make([]string, 0)
			for i, tag := range tagArr {
				if flagArr[i] == 1 {
					remainTags = append(remainTags, fmt.Sprintf("%s=%s", tag[0], tag[1]))
				}

			}
			if maxTime-minTime <= int64(time.Minute.Seconds()) {
				hitKind = 2
				return convertedResponse, byteLength, hitKind
			}

			remainQuery := NewQuery(remainQueryString, DB, "s")
			remainResp, err := conn.Query(remainQuery)
			if err != nil {
				log.Println(remainQueryString)
			}
			if ResponseIsEmpty(remainResp) {
				hitKind = 2

				singleSemanticSegment := GetSingleSegment(metric, partialSegment, remainTags)
				emptyValues := make([]byte, 0)
				for _, ss := range singleSemanticSegment {
					zero, _ := Int64ToByteArray(int64(0))
					emptyValues = append(emptyValues, []byte(ss)...)
					emptyValues = append(emptyValues, []byte(" ")...)
					emptyValues = append(emptyValues, zero...)
				}

				numOfTab := int64(len(singleSemanticSegment))
				err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
				return convertedResponse, byteLength, hitKind
			}

			remainByteArr := RemainResponseToByteArrayWithParams(remainResp, datatypes, remainTags, metric, partialSegment)

			numOfTableR := len(remainResp.Results)

			err = STsConnArr[CacheIndex].Set(&stscache.Item{
				Key:         starSegment,
				Value:       remainByteArr,
				Time_start:  minTime,
				Time_end:    maxTime,
				NumOfTables: int64(numOfTableR),
			})
			totalResp := MergeRemainResponse(remainResp, convertedResponse)

			return totalResp, byteLength, hitKind
		}

	}

}

func STsCacheClientSeg(conn Client, queryString string, semanticSegment string) (*Response, uint64, uint8) {

	CacheNum := len(STsConnArr)

	if CacheNum == 0 {
		CacheNum = 1
	}

	byteLength := uint64(0)
	hitKind := uint8(0)

	queryTemplate, startTime, endTime, tags := GetQueryTemplate(queryString)

	partialSegment := ""
	fields := ""
	metric := ""
	partialSegment, fields, metric = SplitPartialSegment(semanticSegment)

	starSegment := GetStarSegment(metric, partialSegment)

	CacheIndex := GetCacheHashValue(fields)
	fields = "time[int64]," + fields
	datatypes := GetDataTypeArrayFromSF(fields)

	values, _, err := STsConnArr[CacheIndex].Get(semanticSegment, startTime, endTime)
	if err != nil {
		q := NewQuery(queryString, DB, "s")
		resp, err := conn.Query(q)
		if err != nil {
			log.Println(queryString)
		}

		if !ResponseIsEmpty(resp) {
			numOfTab := GetNumOfTable(resp)
			remainValues := ResponseToByteArrayWithParams(resp, datatypes, tags, metric, partialSegment)
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: remainValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		} else {
			singleSemanticSegment := GetSingleSegment(metric, partialSegment, tags)
			emptyValues := make([]byte, 0)
			for _, ss := range singleSemanticSegment {
				zero, _ := Int64ToByteArray(int64(0))
				emptyValues = append(emptyValues, []byte(ss)...)
				emptyValues = append(emptyValues, []byte(" ")...)
				emptyValues = append(emptyValues, zero...)
			}
			numOfTab := int64(len(singleSemanticSegment))
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		}

		return resp, byteLength, hitKind

	} else {
		convertedResponse, flagNum, flagArr, timeRangeArr, tagArr := ByteArrayToResponseWithDatatype(values, datatypes)
		if flagNum == 0 {
			hitKind = 2
			return convertedResponse, byteLength, hitKind
		} else {
			hitKind = 1

			remainQueryString, minTime, maxTime := RemainQueryString(queryString, queryTemplate, flagArr, timeRangeArr, tagArr)

			remainTags := make([]string, 0)
			for i, tag := range tagArr {
				if flagArr[i] == 1 {
					remainTags = append(remainTags, fmt.Sprintf("%s=%s", tag[0], tag[1]))
				}

			}

			if maxTime-minTime <= int64(time.Minute.Seconds()) {
				hitKind = 2
				return convertedResponse, byteLength, hitKind
			}

			remainQuery := NewQuery(remainQueryString, DB, "s")
			remainResp, err := conn.Query(remainQuery)
			if err != nil {
				log.Println(remainQueryString)
			}
			if ResponseIsEmpty(remainResp) {
				hitKind = 1

				singleSemanticSegment := GetSingleSegment(metric, partialSegment, remainTags)
				emptyValues := make([]byte, 0)
				for _, ss := range singleSemanticSegment {
					zero, _ := Int64ToByteArray(int64(0))
					emptyValues = append(emptyValues, []byte(ss)...)
					emptyValues = append(emptyValues, []byte(" ")...)
					emptyValues = append(emptyValues, zero...)
				}

				numOfTab := int64(len(singleSemanticSegment))
				err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
				return convertedResponse, byteLength, hitKind
			}

			remainByteArr := RemainResponseToByteArrayWithParams(remainResp, datatypes, remainTags, metric, partialSegment)
			numOfTableR := len(remainResp.Results)

			err = STsConnArr[CacheIndex].Set(&stscache.Item{
				Key:         starSegment,
				Value:       remainByteArr,
				Time_start:  minTime,
				Time_end:    maxTime,
				NumOfTables: int64(numOfTableR),
			})
			totalResp := MergeRemainResponse(remainResp, convertedResponse)

			return totalResp, byteLength, hitKind
		}

	}

}

var DbConn Client
var TotalLatency int64
var TotalCount int64

func init() {
	DbConn, err = NewHTTPClient(HTTPConfig{
		Addr: "http://192.168.1.101:8086",
	})
	STsConnArr = InitStsConnsArr([]string{"192.168.1.102:11211"})
	TagKV = GetTagKV(DbConn, "devops_small")
	Fields = GetFieldKeys(DbConn, "devops_small")
}

func STsCacheClientSegGrafana(dbName string, queryString string, semanticSegment string) (*Response, uint64, uint8) {

	byteLength := uint64(0)
	hitKind := uint8(0)

	queryTemplate, startTime, endTime, tags := GetQueryTemplate(queryString)

	partialSegment := ""
	fields := ""
	metric := ""
	partialSegment, fields, metric = SplitPartialSegment(semanticSegment)

	starSegment := GetStarSegment(metric, partialSegment)

	fields = "time[int64]," + fields
	datatypes := GetDataTypeArrayFromSF(fields)

	values, _, err := STsConnArr[0].Get(semanticSegment, startTime, endTime)
	if err != nil {
		q := NewQuery(queryString, dbName, "s")
		resp, err := DbConn.Query(q)
		if err != nil {
			log.Println(queryString)
		}

		if !ResponseIsEmpty(resp) {
			numOfTab := GetNumOfTable(resp)
			remainValues := ResponseToByteArrayWithParams(resp, datatypes, tags, metric, partialSegment)
			err = STsConnArr[0].Set(&stscache.Item{Key: starSegment, Value: remainValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		} else {
			singleSemanticSegment := GetSingleSegment(metric, partialSegment, tags)
			emptyValues := make([]byte, 0)
			for _, ss := range singleSemanticSegment {
				zero, _ := Int64ToByteArray(int64(0))
				emptyValues = append(emptyValues, []byte(ss)...)
				emptyValues = append(emptyValues, []byte(" ")...)
				emptyValues = append(emptyValues, zero...)
			}
			numOfTab := int64(len(singleSemanticSegment))
			err = STsConnArr[0].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
		}

		return resp, byteLength, hitKind

	} else {
		convertedResponse, flagNum, flagArr, timeRangeArr, tagArr := ByteArrayToResponseWithDatatype(values, datatypes)
		if flagNum == 0 {
			hitKind = 2
			return convertedResponse, byteLength, hitKind
		} else {
			hitKind = 1

			remainQueryString, minTime, maxTime := RemainQueryString(queryString, queryTemplate, flagArr, timeRangeArr, tagArr)

			remainTags := make([]string, 0)
			for i, tag := range tagArr {
				if flagArr[i] == 1 {
					remainTags = append(remainTags, fmt.Sprintf("%s=%s", tag[0], tag[1]))
				}

			}

			if maxTime-minTime <= int64(time.Minute.Seconds()) {
				hitKind = 2
				return convertedResponse, byteLength, hitKind
			}

			remainQuery := NewQuery(remainQueryString, dbName, "s")
			remainResp, err := DbConn.Query(remainQuery)
			if err != nil {
				log.Println(remainQueryString)
			}
			if ResponseIsEmpty(remainResp) {
				hitKind = 1

				singleSemanticSegment := GetSingleSegment(metric, partialSegment, remainTags)
				emptyValues := make([]byte, 0)
				for _, ss := range singleSemanticSegment {
					zero, _ := Int64ToByteArray(int64(0))
					emptyValues = append(emptyValues, []byte(ss)...)
					emptyValues = append(emptyValues, []byte(" ")...)
					emptyValues = append(emptyValues, zero...)
				}

				numOfTab := int64(len(singleSemanticSegment))
				err = STsConnArr[0].Set(&stscache.Item{Key: starSegment, Value: emptyValues, Time_start: startTime, Time_end: endTime, NumOfTables: numOfTab})
				return convertedResponse, byteLength, hitKind
			}

			remainByteArr := RemainResponseToByteArrayWithParams(remainResp, datatypes, remainTags, metric, partialSegment)
			numOfTableR := len(remainResp.Results)

			err = STsConnArr[0].Set(&stscache.Item{
				Key:         starSegment,
				Value:       remainByteArr,
				Time_start:  minTime,
				Time_end:    maxTime,
				NumOfTables: int64(numOfTableR),
			})
			totalResp := MergeRemainResponse(remainResp, convertedResponse)

			return totalResp, byteLength, hitKind
		}

	}

}
