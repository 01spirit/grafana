package influxdb_client

import (
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana/InfluxDB-client/models"
	"github.com/influxdata/influxql"
	"log"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

func ResponseIsEmpty(resp *Response) bool {
	return resp == nil || resp.Results == nil || len(resp.Results[0].Series) == 0 || len(resp.Results[0].Series[0].Values) == 0 || len(resp.Results[0].Series[0].Values[0]) == 0
}

func GetNumOfTable(resp *Response) int64 {
	return int64(len(resp.Results[0].Series))
}

func GetResponseTimeRange(resp *Response) (int64, int64) {
	var minStartTime int64
	var maxEndTime int64
	var ist int64
	var iet int64

	if ResponseIsEmpty(resp) {
		return -1, -1
	}
	minStartTime = math.MaxInt64
	maxEndTime = 0
	for s := range resp.Results[0].Series {
		length := len(resp.Results[0].Series[s].Values)

		if len(resp.Results[0].Series[s].Values) == 0 {
			continue
		}
		start := resp.Results[0].Series[s].Values[0][0]
		end := resp.Results[0].Series[s].Values[length-1][0]

		if st, ok := start.(string); ok {
			et := end.(string)
			ist = TimeStringToInt64(st)
			iet = TimeStringToInt64(et)
		} else if st, ok := start.(json.Number); ok {
			et, ok := end.(json.Number)
			if !ok {
				continue
			} else {
				ist, _ = st.Int64()
				iet, _ = et.Int64()
			}

		}
		if minStartTime > ist {
			minStartTime = ist
		}
		if maxEndTime < iet {
			maxEndTime = iet
		}
	}

	return minStartTime, maxEndTime
}

func GetSeriesTimeRange(series models.Row) (int64, int64, error) {
	var stime int64
	var etime int64

	if len(series.Values) == 0 {
		return 0, 0, fmt.Errorf("empty series")
	}

	start := series.Values[0][0]
	end := series.Values[len(series.Values)-1][0]

	st := start.(json.Number)
	et := end.(json.Number)
	stime, _ = st.Int64()
	etime, _ = et.Int64()

	return stime, etime, nil
}

func GetQueryTimeRange(queryString string) (int64, int64) {
	matchStr := `(?i).+WHERE(.+)`
	conditionExpr := regexp.MustCompile(matchStr)
	if ok, _ := regexp.MatchString(matchStr, queryString); !ok {
		return -1, -1
	}
	condExprMatch := conditionExpr.FindStringSubmatch(queryString)
	parseExpr := condExprMatch[1]

	now := time.Now()
	valuer := influxql.NowValuer{Now: now}
	expr, _ := influxql.ParseExpr(parseExpr)
	_, timeRange, err := influxql.ConditionExpr(expr, &valuer)

	if err != nil {
		return -1, -1
	}

	startTime := timeRange.MinTime().Unix()
	endTime := timeRange.MaxTime().Unix()

	if startTime <= 0 || startTime*1000000000 < (math.MinInt64/2) {
		startTime = -1
	}
	if endTime <= 0 || endTime*1000000000 > (math.MaxInt64/2) {
		endTime = -1
	}

	if endTime != -1 && endTime != startTime && !strings.Contains(queryString, "<=") {
		endTime++
	}

	return startTime, endTime
}

func GetQueryTemplate(queryString string) (string, int64, int64, []string) {
	var startTime int64
	var endTime int64
	var tags []string

	timeReg := regexp.MustCompile("[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z")
	replacement := "?"

	times := timeReg.FindAllString(queryString, -1)
	if len(times) == 0 {
		startTime = 0
		endTime = 0
	} else if len(times) == 1 {
		startTime = TimeStringToInt64(times[0])
		endTime = startTime
	} else {
		startTime = TimeStringToInt64(times[0])
		endTime = TimeStringToInt64(times[1])
	}

	result := timeReg.ReplaceAllString(queryString, replacement)

	if strings.Contains(queryString, "WHERE TIME") {
		return result, startTime, endTime, tags
	}

	tagReg := `(?i)WHERE \((.+)\) AND`
	conditionExpr := regexp.MustCompile(tagReg)
	if ok, _ := regexp.MatchString(tagReg, queryString); !ok {
		return "", 0, 0, nil
	}
	tagExprMatch := conditionExpr.FindStringSubmatch(result)
	tagString := tagExprMatch[1]
	result = strings.ReplaceAll(result, tagString, replacement)

	tagString = strings.ReplaceAll(tagString, "\"", "")
	tagString = strings.ReplaceAll(tagString, "'", "")
	tagString = strings.ReplaceAll(tagString, " ", "")

	tags = strings.Split(tagString, "or")
	sort.Strings(tags)

	return result, startTime, endTime, tags
}

func GetFieldKeys(c Client, database string) map[string]map[string]string {
	query := fmt.Sprintf("SHOW FIELD KEYS on \"%s\"", database)

	q := NewQuery(query, database, "")
	resp, err := c.Query(q)
	if err != nil {
		fmt.Printf("Error: %s\n", err.Error())
		return nil
	}

	if resp.Error() != nil {
		fmt.Printf("Error: %s\n", resp.Error().Error())
		return nil
	}

	fieldMap := make(map[string]map[string]string)
	for _, series := range resp.Results[0].Series {
		fieldNames := make([]string, 0)
		datatypes := make([]string, 0)
		measurementName := series.Name
		for _, value := range series.Values {
			fieldName, ok := value[0].(string)
			datatype, ok := value[1].(string)
			if !ok {
				log.Fatal("field and datatype name fail to convert to string")
			}
			if datatype == "float" {
				datatype = "float64"
			} else if datatype == "integer" {
				datatype = "int64"
			}
			fieldNames = append(fieldNames, fieldName)
			datatypes = append(datatypes, datatype)
		}
		fldMap := make(map[string]string)
		for i := range fieldNames {
			fldMap[fieldNames[i]] = datatypes[i]
		}

		fieldMap[measurementName] = fldMap
	}

	return fieldMap
}

type TagValues struct {
	Values []string
}

type TagKeyMap struct {
	Tag map[string]TagValues
}

type MeasurementTagMap struct {
	Measurement map[string][]TagKeyMap
}

func GetTagKV(c Client, database string) MeasurementTagMap {
	queryK := fmt.Sprintf("SHOW tag KEYS on \"%s\"", database)
	q := NewQuery(queryK, database, "")
	resp, err := c.Query(q)
	if err != nil {
		log.Fatal(err.Error())
	}
	if resp.Error() != nil {
		log.Fatal(resp.Error().Error())
	}

	tagMap := make(map[string][]string)
	for _, series := range resp.Results[0].Series {
		measurementName := series.Name
		for _, value := range series.Values {
			tagKey, ok := value[0].(string)
			if !ok {
				log.Fatal("tag name fail to convert to string")
			}
			tagMap[measurementName] = append(tagMap[measurementName], tagKey)
		}
	}

	var measurementTagMap MeasurementTagMap
	measurementTagMap.Measurement = make(map[string][]TagKeyMap)
	for k, v := range tagMap {
		for _, tagKey := range v {
			queryV := fmt.Sprintf("SHOW tag VALUES on \"%s\" from \"%s\" with key=\"%s\"", database, k, tagKey)
			q := NewQuery(queryV, database, "")
			resp, err := c.Query(q)
			if err != nil {
				log.Fatal(err.Error())
			}
			if resp.Error() != nil {
				log.Fatal(resp.Error().Error())
			}

			var tagValues TagValues
			for _, value := range resp.Results[0].Series[0].Values {
				tagValues.Values = append(tagValues.Values, value[1].(string))
			}
			tmpKeyMap := make(map[string]TagValues, 0)
			tmpKeyMap[tagKey] = tagValues
			tagKeyMap := TagKeyMap{tmpKeyMap}
			measurementTagMap.Measurement[k] = append(measurementTagMap.Measurement[k], tagKeyMap)
		}
	}

	return measurementTagMap
}

func GetTagNameArr(resp *Response) []string {
	tagArr := make([]string, 0)
	if resp == nil || len(resp.Results[0].Series) == 0 {
		return tagArr
	} else {
		if len(resp.Results[0].Series[0].Values) == 0 {
			return tagArr
		} else {
			for k, _ := range resp.Results[0].Series[0].Tags {
				tagArr = append(tagArr, k)
			}
		}
	}
	sort.Strings(tagArr)
	return tagArr
}

func GetDataTypeArrayFromSF(sfString string) []string {
	datatypes := make([]string, 0)
	columns := strings.Split(sfString, ",")

	for _, col := range columns {
		startIdx := strings.Index(col, "[") + 1
		endIdx := strings.Index(col, "]")
		datatypes = append(datatypes, col[startIdx:endIdx])
	}

	return datatypes
}

func GetDataTypeArrayFromResponse(resp *Response) []string {
	fields := make([]string, 0)
	done := false
	able := false
	for _, s := range resp.Results[0].Series {
		if done {
			break
		}
		for _, v := range s.Values {
			if done {
				break
			}
			for _, vv := range v {
				if vv == nil {
					able = false
					break
				}
				able = true
			}
			if able {
				for i, value := range v {
					if i == 0 {
						fields = append(fields, "int64")
					} else if _, ok := value.(string); ok {
						fields = append(fields, "string")
					} else if v, ok := value.(json.Number); ok {
						if _, err := v.Int64(); err == nil {
							fields = append(fields, "float64") // todo
						} else if _, err := v.Float64(); err == nil {
							fields = append(fields, "float64")
						} else {
							fields = append(fields, "string")
						}
					} else if _, ok := value.(bool); ok {
						fields = append(fields, "bool")
					}
					done = true
				}
			}

		}
	}

	return fields
}
