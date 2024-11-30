package timescaledb_client

import (
	"database/sql"
	"fmt"
	stscache "github.com/grafana/grafana/TimescaleDB_Client/stscache_client"
	"log"
	"strings"
	"sync"
	"time"
)

const pgxDriver = "pgx"

var DB = "devops_small"
var DbName = ""
var STsCacheURL string
var UseCache = "db"
var MaxThreadNum = 64

const STRINGBYTELENGTH = 32

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

func InitStsConnsArr(urlArr []string) []*stscache.Client {
	conns := make([]*stscache.Client, 0)
	for i := 0; i < len(urlArr); i++ {
		conns = append(conns, stscache.New(urlArr[i]))
	}
	return conns
}

func init() {
	STsConnArr = InitStsConnsArr([]string{"192.168.1.101"})
}

func STsCacheClientSeg(conn *sql.DB, queryString string, semanticSegment string) ([][][]interface{}, uint64, uint8) {

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

	CacheIndex := 0
	fields = "time[int64],name[string]," + fields
	colLen := strings.Split(fields, ",")
	datatypes := DataTypeFromColumn(len(colLen))

	values, _, err := STsConnArr[CacheIndex].Get(semanticSegment, startTime, endTime)
	if err != nil {
		rows, err := conn.Query(queryString)
		if err != nil {
			log.Println(queryString)
		}

		var dataArray [][]interface{} = nil
		if !ResponseIsEmpty(rows) {
			dataArray = RowsToInterface(rows, len(datatypes))
			remainValues, numOfTab := ResponseInterfaceToByteArrayWithParams(dataArray, datatypes, tags, metric, partialSegment)
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

		return [][][]interface{}{dataArray}, byteLength, hitKind

	} else {
		convertedResponse, flagNum, flagArr, timeRangeArr, tagArr := ByteArrayToResponseWithDatatype(values, datatypes)
		if flagNum == 0 {
			hitKind = 2
			return convertedResponse, byteLength, hitKind
		} else {
			hitKind = 1
			remainQueryString, minTime, maxTime := RemainQuery(queryTemplate, flagArr, timeRangeArr, tagArr)
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
			remainRows, err := conn.Query(remainQueryString)
			if err != nil {
				log.Println(remainQueryString)
			}
			if ResponseIsEmpty(remainRows) {
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

			remainDataArray := RowsToInterface(remainRows, len(datatypes))
			remainByteArr, numOfTableR := ResponseInterfaceToByteArrayWithParams(remainDataArray, datatypes, remainTags, metric, partialSegment)
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: remainByteArr, Time_start: minTime, Time_end: maxTime, NumOfTables: int64(numOfTableR)})
			totalResp := MergeRemainResponse(convertedResponse, remainDataArray, timeRangeArr)
			return totalResp, byteLength, hitKind
		}

	}

}

func GrafanaSTsCacheClientSeg(conn *sql.DB, queryString string, semanticSegment string) ([][][]interface{}, []string, []string, error) {

	CacheNum := len(STsConnArr)

	if CacheNum == 0 {
		CacheNum = 1
	}

	queryTemplate, startTime, endTime, tags := GetQueryTemplate(queryString)

	partialSegment := ""
	fields := ""
	metric := ""
	partialSegment, fields, metric = SplitPartialSegment(semanticSegment)

	starSegment := GetStarSegment(metric, partialSegment)

	CacheIndex := 0
	fields = "time[int64],name[string]," + fields
	columns := strings.Split(fields, ",")
	for i, col := range columns {
		columns[i] = col[:strings.Index(col, "[")]
	}
	datatypes := DataTypeFromColumn(len(columns))

	var dataArray [][]interface{} = nil
	rows, err := conn.Query(queryString)
	if !ResponseIsEmpty(rows) {
		dataArray = RowsToInterface(rows, len(datatypes))
	}
	return [][][]interface{}{dataArray}, columns, datatypes, err

	values, _, err := STsConnArr[CacheIndex].Get(semanticSegment, startTime, endTime)
	if err != nil {
		rows, err := conn.Query(queryString)
		if err != nil {
			log.Println(queryString)
		}

		var dataArray [][]interface{} = nil
		if !ResponseIsEmpty(rows) {
			dataArray = RowsToInterface(rows, len(datatypes))
			remainValues, numOfTab := ResponseInterfaceToByteArrayWithParams(dataArray, datatypes, tags, metric, partialSegment)
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

		return [][][]interface{}{dataArray}, columns, datatypes, err

	} else {
		convertedResponse, flagNum, flagArr, timeRangeArr, tagArr := ByteArrayToResponseWithDatatype(values, datatypes)
		if flagNum == 0 {
			return convertedResponse, columns, datatypes, err
		} else {
			remainQueryString, minTime, maxTime := RemainQuery(queryTemplate, flagArr, timeRangeArr, tagArr)
			remainTags := make([]string, 0)
			for i, tag := range tagArr {
				if flagArr[i] == 1 {
					remainTags = append(remainTags, fmt.Sprintf("%s=%s", tag[0], tag[1]))
				}
			}
			if maxTime-minTime <= int64(time.Minute.Seconds()) {
				return convertedResponse, columns, datatypes, err
			}
			remainRows, err := conn.Query(remainQueryString)
			if err != nil {
				log.Println(remainQueryString)
			}
			if ResponseIsEmpty(remainRows) {
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
				return convertedResponse, columns, datatypes, err
			}

			remainDataArray := RowsToInterface(remainRows, len(datatypes))
			remainByteArr, numOfTableR := ResponseInterfaceToByteArrayWithParams(remainDataArray, datatypes, remainTags, metric, partialSegment)
			err = STsConnArr[CacheIndex].Set(&stscache.Item{Key: starSegment, Value: remainByteArr, Time_start: minTime, Time_end: maxTime, NumOfTables: int64(numOfTableR)})
			totalResp := MergeRemainResponse(convertedResponse, remainDataArray, timeRangeArr)
			return totalResp, columns, datatypes, err
		}

	}

}
