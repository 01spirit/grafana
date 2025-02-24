package influxdb_client

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/grafana/grafana/InfluxDB-client/models"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (resp *Response) ToString() string {
	var result string
	var tags []string

	tags = GetTagNameArr(resp)
	if ResponseIsEmpty(resp) {
		return "empty response"
	}

	for r := range resp.Results {

		for s := range resp.Results[r].Series {

			result += "SCHEMA "
			for c := range resp.Results[r].Series[s].Columns {
				result += resp.Results[r].Series[s].Columns[c]
				result += " "
			}

			for t, tag := range tags {
				result += fmt.Sprintf("%s=%s ", tags[t], resp.Results[r].Series[s].Tags[tag])
			}
			result += "\r\n"

			for v := range resp.Results[r].Series[s].Values {
				for vv := range resp.Results[r].Series[s].Values[v] {
					if resp.Results[r].Series[s].Values[v][vv] == nil {
						result += "_"
					} else if str, ok := resp.Results[r].Series[s].Values[v][vv].(string); ok {
						result += str
					} else if jsonNumber, ok := resp.Results[r].Series[s].Values[v][vv].(json.Number); ok {
						str := jsonNumber.String()
						result += str
					} else {
						result += "#"
					}
					result += " "
				}
				result += "\r\n"
			}
		}
	}
	result += "end"
	return result
}

func RemainResponseToByteArrayWithParams(resp *Response, datatypes []string, tags []string, metric string, partialSegment string) []byte {
	byteArray := make([]byte, 0)

	if ResponseIsEmpty(resp) {
		//return StringToByteArray("empty response")
		return nil
	}

	bytesPerLine := BytesPerLine(datatypes)

	index := 0
	for _, result := range resp.Results {
		if result.Series == nil {
			emptySingleSegment := fmt.Sprintf("{(%s.%s)}%s", metric, tags[index], partialSegment)
			zero, _ := Int64ToByteArray(int64(0))
			byteArray = append(byteArray, []byte(emptySingleSegment)...)
			byteArray = append(byteArray, []byte(" ")...)
			byteArray = append(byteArray, zero...)

			index++
			continue
		}
		numOfValues := len(result.Series[0].Values)
		bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * numOfValues))

		singleSegment := ""
		if len(result.Series[0].Tags) == 0 {
			singleSegment = fmt.Sprintf("{(%s.*)}%s", metric, partialSegment)
			index++
		} else {
			for key, value := range result.Series[0].Tags {
				singleSegment = fmt.Sprintf("{(%s.%s=%s)}%s", metric, key, value, partialSegment)
				startIndex := strings.Index(tags[index], "=")
				for index < len(tags) {
					if !strings.EqualFold(value, tags[index][startIndex+1:]) {
						emptySingleSegment := fmt.Sprintf("{(%s.%s)}%s", metric, tags[index], partialSegment)
						zero, _ := Int64ToByteArray(int64(0))
						byteArray = append(byteArray, []byte(emptySingleSegment)...)
						byteArray = append(byteArray, []byte(" ")...)
						byteArray = append(byteArray, zero...)

						index++
					} else {
						index++
						break
					}
				}
				break
			}
		}

		byteArray = append(byteArray, []byte(singleSegment)...)
		byteArray = append(byteArray, []byte(" ")...)
		byteArray = append(byteArray, bytesPerSeries...)

		for _, v := range result.Series[0].Values {
			for j, vv := range v {
				datatype := datatypes[j]
				if j != 0 {
					datatype = "float64"
				}
				tmpBytes := InterfaceToByteArray(j, datatype, vv)
				byteArray = append(byteArray, tmpBytes...)

			}
		}
	}

	for index < len(tags) {

		emptySingleSegment := fmt.Sprintf("{(%s.%s)}%s", metric, tags[index], partialSegment)
		zero, _ := Int64ToByteArray(int64(0))
		byteArray = append(byteArray, []byte(emptySingleSegment)...)
		byteArray = append(byteArray, []byte(" ")...)
		byteArray = append(byteArray, zero...)

		index++

	}

	return byteArray
}

func ResponseToByteArrayWithParams(resp *Response, datatypes []string, tags []string, metric string, partialSegment string) []byte {
	result := make([]byte, 0)

	if ResponseIsEmpty(resp) {
		//return StringToByteArray("empty response")
		return nil
	}

	singleSegments := GetSingleSegment(metric, partialSegment, tags)

	bytesPerLine := BytesPerLine(datatypes)

	for i, s := range resp.Results[0].Series {
		numOfValues := len(s.Values)
		bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * numOfValues))

		result = append(result, []byte(singleSegments[i])...)
		result = append(result, []byte(" ")...)
		result = append(result, bytesPerSeries...)

		for _, v := range s.Values {
			for j, vv := range v {
				datatype := datatypes[j]
				if j != 0 {
					datatype = "float64"
				}
				tmpBytes := InterfaceToByteArray(j, datatype, vv)
				result = append(result, tmpBytes...)

			}
		}
	}

	return result
}

func ResponseToByteArray(resp *Response, queryString string) []byte {
	result := make([]byte, 0)

	if ResponseIsEmpty(resp) {
		return StringToByteArray("empty response")
	}

	datatypes := make([]string, 0)
	datatypes = append(datatypes, "int64")
	mtx.Lock()

	semanticSegment := ""
	fields := ""
	queryTemplate, _, _, _ := GetQueryTemplate(queryString)
	if ss, ok := QueryTemplates[queryTemplate]; !ok {

		semanticSegment, fields = GetSemanticSegmentAndFields(queryString)

		QueryTemplates[queryTemplate] = semanticSegment
		SegmentToFields[semanticSegment] = fields

	} else {
		semanticSegment = ss
		fields = SegmentToFields[semanticSegment]
	}
	fieldArr := strings.Split(fields, ",")
	for i := range fieldArr {
		startIndex := strings.Index(fieldArr[i], "[")
		endIndex := strings.Index(fieldArr[i], "]")
		datatypes = append(datatypes, fieldArr[i][startIndex+1:endIndex])
	}

	seperateSemanticSegment := GetSeperateSemanticSegment(queryString)
	nullTags := make([]string, 0)
	if len(seperateSemanticSegment) < len(resp.Results[0].Series) {

		tagMap := resp.Results[0].Series[0].Tags
		for key, val := range tagMap {
			if val == "" {
				nullTags = append(nullTags, key)
			}
		}
	}
	nullSegment := GetSeparateSemanticSegmentWithNullTag(seperateSemanticSegment[0], nullTags)
	newSepSeg := make([]string, 0)
	if nullSegment == "" {
		newSepSeg = append(newSepSeg, seperateSemanticSegment...)
	} else {
		newSepSeg = append(newSepSeg, nullSegment)
		newSepSeg = append(newSepSeg, seperateSemanticSegment...)
	}
	mtx.Unlock()
	bytesPerLine := BytesPerLine(datatypes)

	for i, s := range resp.Results[0].Series {
		numOfValues := len(s.Values)
		bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * numOfValues))

		result = append(result, []byte(newSepSeg[i])...)
		result = append(result, []byte(" ")...)
		result = append(result, bytesPerSeries...)
		for _, v := range s.Values {
			for j, vv := range v {
				datatype := datatypes[j]
				tmpBytes := InterfaceToByteArray(j, datatype, vv)
				result = append(result, tmpBytes...)

			}
		}
	}

	return result
}

func ByteArrayToResponseWithDatatype(byteArray []byte, datatypes []string) (*Response, int, []uint8, [][]int64, [][]string) {

	if len(byteArray) == 0 {
		return nil, 0, nil, nil, nil
	}

	valuess := make([][][]interface{}, 0)
	values := make([][]interface{}, 0)
	value := make([]interface{}, 0)

	seprateSemanticSegments := make([]string, 0)
	seriesLength := make([]int64, 0)

	flagNum := 0
	flagArr := make([]uint8, 0)
	timeRangeArr := make([][]int64, 0)
	tagArr := make([][]string, 0)

	var curSeg string
	var curLen int64
	index := 0
	length := len(byteArray)

	per_index := 0
	is_start := 0

	for index < length {
		if index == length-2 {
			if byteArray[index] == 13 && byteArray[index+1] == 10 {
				break
			} else {
				log.Fatal(errors.New("expect CRLF in the end of []byte"))
			}
		}
		if is_start == 1 && per_index == index {
			return nil, 0, nil, nil, nil
		}
		is_start = 1
		per_index = index

		curSeg = ""
		curLen = 0
		if byteArray[index] == 123 && byteArray[index+1] == 40 {
			ssStartIdx := index
			for byteArray[index] != 32 {
				index++
			}
			ssEndIdx := index
			curSeg = string(byteArray[ssStartIdx:ssEndIdx])
			seprateSemanticSegments = append(seprateSemanticSegments, curSeg)

			index++
			flag := uint8(byteArray[index])
			index++
			flagArr = append(flagArr, flag)
			if flag == 1 {
				flagNum++
				singleTimeRange := make([]int64, 2)
				ftimeStartIdx := index
				index += 8
				ftimeEndIdx := index
				tmpBytes := byteArray[ftimeStartIdx:ftimeEndIdx]
				startTime, err := ByteArrayToInt64(tmpBytes)
				if err != nil {
					log.Fatal(err)
				}
				singleTimeRange[0] = startTime

				stimeStartIdx := index
				index += 8
				stimeEndIdx := index
				tmpBytes = byteArray[stimeStartIdx:stimeEndIdx]
				endTime, err := ByteArrayToInt64(tmpBytes)
				if err != nil {
					log.Fatal(err)
				}
				singleTimeRange[1] = endTime

				timeRangeArr = append(timeRangeArr, singleTimeRange)
			} else {
				singleTimeRange := make([]int64, 2)
				singleTimeRange[0] = 0
				singleTimeRange[1] = 0
				timeRangeArr = append(timeRangeArr, singleTimeRange)
			}

			lenStartIdx := index
			index += 8
			lenEndIdx := index
			tmpBytes := byteArray[lenStartIdx:lenEndIdx]
			serLen, err := ByteArrayToInt64(tmpBytes)
			if err != nil {
				log.Fatal(err)
			}
			curLen = serLen
			seriesLength = append(seriesLength, curLen)

		}

		bytesPerLine := BytesPerLine(datatypes)
		lines := int(curLen) / bytesPerLine
		values = nil
		for len(values) < lines {
			value = nil
			for i, d := range datatypes {
				if i != 0 {
					d = "float64"
				}
				switch d {
				case "string":
					bStartIdx := index
					index += STRINGBYTELENGTH
					bEndIdx := index
					tmp := ByteArrayToString(byteArray[bStartIdx:bEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tmp)
					break
				case "bool":
					bStartIdx := index
					index += 1
					bEndIdx := index
					tmp, err := ByteArrayToBool(byteArray[bStartIdx:bEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tmp)
					break
				case "int64":
					iStartIdx := index
					index += 8
					iEndIdx := index
					tmp, err := ByteArrayToInt64(byteArray[iStartIdx:iEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					str := strconv.FormatInt(tmp, 10)
					jNumber := json.Number(str)
					value = append(value, jNumber)
					//tmp := binary.LittleEndian.Uint64(byteArray[iStartIdx:iEndIdx])
					//value = append(value, tmp)
					break
				case "float64":
					fStartIdx := index
					index += 8
					fEndIdx := index
					tmp, err := ByteArrayToFloat64(byteArray[fStartIdx:fEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					str := strconv.FormatFloat(tmp, 'g', -1, 64)
					jNumber := json.Number(str)
					value = append(value, jNumber)
					//tmp := binary.LittleEndian.Uint64(byteArray[fStartIdx:fEndIdx])
					//floatValue := math.Float64frombits(tmp)
					//jsonNumber := json.Number(strconv.FormatFloat(floatValue, 'f', -1, 64))
					//value = append(value, floatValue)
					break
				default:
					sStartIdx := index
					index += 8
					sEndIdx := index
					tmp, err := ByteArrayToFloat64(byteArray[sStartIdx:sEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tmp)
					break
				}
			}
			values = append(values, value)
		}
		valuess = append(valuess, values)
	}
	modelsRows := make([]models.Row, 0)

	for i, s := range seprateSemanticSegments {
		messages := strings.Split(s, "#")
		ssm := messages[0][2 : len(messages[0])-2]
		merged := strings.Split(ssm, ",")
		nameIndex := strings.Index(merged[0], ".")
		name := merged[0][:nameIndex]
		tags := make(map[string]string)
		tag := merged[0][nameIndex+1 : len(merged[0])]
		eqIdx := strings.Index(tag, "=")
		if eqIdx <= 0 {
			if tag == "*" {

			} else {
				break
			}

		} else {
			key := tag[:eqIdx]
			val := tag[eqIdx+1 : len(tag)]
			tags[key] = val

			tmpTagArr := make([]string, 2)
			tmpTagArr[0] = key
			tmpTagArr[1] = val
			tagArr = append(tagArr, tmpTagArr)
		}

		columns := make([]string, 0)
		sf := "time[int64],"
		sf += messages[1][1 : len(messages[1])-1]
		sg := messages[3][1 : len(messages[3])-1]
		splitSg := strings.Split(sg, ",")
		aggr := splitSg[0]
		if strings.Compare(aggr, "empty") != 0 {
			columns = append(columns, "time")
			columns = append(columns, aggr)
			fields := strings.Split(sf, ",")
			if len(fields) > 2 {
				for j := 1; j < len(fields)-1; j++ {
					columns = append(columns, fmt.Sprintf("%s_%d", aggr, j))
				}
			}
		} else {
			fields := strings.Split(sf, ",")
			for _, f := range fields {
				idx := strings.Index(f, "[")
				columnName := f[:idx]
				columns = append(columns, columnName)
			}
		}

		seriesTmp := Series{
			Name:    name,
			Tags:    tags,
			Columns: columns,
			Values:  valuess[i],
			Partial: false,
		}

		row := SeriesToRow(seriesTmp)
		modelsRows = append(modelsRows, row)
	}

	result := Result{
		StatementId: 0,
		Series:      modelsRows,
		Messages:    nil,
		Err:         "",
	}
	resp := Response{
		Results: []Result{result},
		Err:     "",
	}

	return &resp, flagNum, flagArr, timeRangeArr, tagArr
}

func ByteArrayToResponse(byteArray []byte) (*Response, int, []uint8, [][]int64, [][]string) {

	if len(byteArray) == 0 {
		return nil, 0, nil, nil, nil
	}

	valuess := make([][][]interface{}, 0)
	values := make([][]interface{}, 0)
	value := make([]interface{}, 0)

	seprateSemanticSegments := make([]string, 0)
	seriesLength := make([]int64, 0)

	flagNum := 0
	flagArr := make([]uint8, 0)
	timeRangeArr := make([][]int64, 0)
	tagArr := make([][]string, 0)

	var curSeg string
	var curLen int64
	index := 0
	length := len(byteArray)

	for index < length {
		if index == length-2 {
			if byteArray[index] == 13 && byteArray[index+1] == 10 {
				break
			} else {
				log.Fatal(errors.New("expect CRLF in the end of []byte"))
			}
		}

		curSeg = ""
		curLen = 0
		if byteArray[index] == 123 && byteArray[index+1] == 40 {
			ssStartIdx := index
			for byteArray[index] != 32 {
				index++
			}
			ssEndIdx := index
			curSeg = string(byteArray[ssStartIdx:ssEndIdx])
			seprateSemanticSegments = append(seprateSemanticSegments, curSeg)

			index++
			flag := uint8(byteArray[index])
			index++
			flagArr = append(flagArr, flag)
			if flag == 1 {
				flagNum++
				singleTimeRange := make([]int64, 2)
				ftimeStartIdx := index
				index += 8
				ftimeEndIdx := index
				tmpBytes := byteArray[ftimeStartIdx:ftimeEndIdx]
				startTime, err := ByteArrayToInt64(tmpBytes)
				if err != nil {
					log.Fatal(err)
				}
				singleTimeRange[0] = startTime

				stimeStartIdx := index
				index += 8
				stimeEndIdx := index
				tmpBytes = byteArray[stimeStartIdx:stimeEndIdx]
				endTime, err := ByteArrayToInt64(tmpBytes)
				if err != nil {
					log.Fatal(err)
				}
				singleTimeRange[1] = endTime

				timeRangeArr = append(timeRangeArr, singleTimeRange)
			} else {
				singleTimeRange := make([]int64, 2)
				singleTimeRange[0] = 0
				singleTimeRange[1] = 0
				timeRangeArr = append(timeRangeArr, singleTimeRange)
			}

			lenStartIdx := index
			index += 8
			lenEndIdx := index
			tmpBytes := byteArray[lenStartIdx:lenEndIdx]
			serLen, err := ByteArrayToInt64(tmpBytes)
			if err != nil {
				log.Fatal(err)
			}
			curLen = serLen
			seriesLength = append(seriesLength, curLen)

		}

		sf := "time[int64],"
		messages := strings.Split(curSeg, "#")
		sf += messages[1][1 : len(messages[1])-1]
		datatypes := GetDataTypeArrayFromSF(sf)

		bytesPerLine := BytesPerLine(datatypes)
		lines := int(curLen) / bytesPerLine
		values = nil
		for len(values) < lines {
			value = nil
			for _, d := range datatypes {
				switch d {
				case "bool":
					bStartIdx := index
					index += 1
					bEndIdx := index
					tmp, err := ByteArrayToBool(byteArray[bStartIdx:bEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tmp)
					break
				case "int64":
					iStartIdx := index
					index += 8
					iEndIdx := index
					tmp, err := ByteArrayToInt64(byteArray[iStartIdx:iEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					str := strconv.FormatInt(tmp, 10)
					jNumber := json.Number(str)
					value = append(value, jNumber)
					break
				case "float64":
					fStartIdx := index
					index += 8
					fEndIdx := index
					tmp, err := ByteArrayToFloat64(byteArray[fStartIdx:fEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					str := strconv.FormatFloat(tmp, 'g', -1, 64)
					jNumber := json.Number(str)
					value = append(value, jNumber)
					break
				default:
					sStartIdx := index
					index += STRINGBYTELENGTH
					sEndIdx := index
					tmp := ByteArrayToString(byteArray[sStartIdx:sEndIdx])
					value = append(value, tmp)
					break
				}
			}
			values = append(values, value)
		}
		valuess = append(valuess, values)
	}
	modelsRows := make([]models.Row, 0)

	for i, s := range seprateSemanticSegments {
		messages := strings.Split(s, "#")

		ssm := messages[0][2 : len(messages[0])-2]
		merged := strings.Split(ssm, ",")
		nameIndex := strings.Index(merged[0], ".")
		name := merged[0][:nameIndex]
		tags := make(map[string]string)
		tag := merged[0][nameIndex+1 : len(merged[0])]
		eqIdx := strings.Index(tag, "=")
		if eqIdx <= 0 {
			break
		}
		key := tag[:eqIdx]
		val := tag[eqIdx+1 : len(tag)]
		tags[key] = val

		tmpTagArr := make([]string, 2)
		tmpTagArr[0] = key
		tmpTagArr[1] = val
		tagArr = append(tagArr, tmpTagArr)

		columns := make([]string, 0)
		sf := "time[int64],"
		sf += messages[1][1 : len(messages[1])-1]
		sg := messages[3][1 : len(messages[3])-1]
		splitSg := strings.Split(sg, ",")
		aggr := splitSg[0]
		if strings.Compare(aggr, "empty") != 0 {
			columns = append(columns, "time")
			columns = append(columns, aggr)
		} else {
			fields := strings.Split(sf, ",")
			for _, f := range fields {
				idx := strings.Index(f, "[")
				columnName := f[:idx]
				columns = append(columns, columnName)
			}
		}

		seriesTmp := Series{
			Name:    name,
			Tags:    tags,
			Columns: columns,
			Values:  valuess[i],
			Partial: false,
		}

		row := SeriesToRow(seriesTmp)
		modelsRows = append(modelsRows, row)
	}

	result := Result{
		StatementId: 0,
		Series:      modelsRows,
		Messages:    nil,
		Err:         "",
	}
	resp := Response{
		Results: []Result{result},
		Err:     "",
	}

	return &resp, flagNum, flagArr, timeRangeArr, tagArr
}

func RemainQueryString(queryString string, queryTemplate string, flagArr []uint8, timeRangeArr [][]int64, tagArr [][]string) (string, int64, int64) {
	if len(flagArr) == 0 || len(timeRangeArr) == 0 {
		return "", 0, 0
	}

	var maxTime int64 = 0
	var minTime int64 = math.MaxInt64

	if len(tagArr) == 0 {
		minTime = timeRangeArr[0][0]
		maxTime = timeRangeArr[0][1]

		tmpTemplate := queryTemplate
		startTime := TimeInt64ToString(timeRangeArr[0][0])
		endTime := TimeInt64ToString(timeRangeArr[0][1])
		tmpTemplate = strings.Replace(tmpTemplate, "?", startTime, 1)
		tmpTemplate = strings.Replace(tmpTemplate, "?", endTime, 1)

		return tmpTemplate, minTime, maxTime
	}
	tagName := tagArr[0][0]
	matchStr := `(?i)(.+)WHERE.+`
	conditionExpr := regexp.MustCompile(matchStr)
	if ok, _ := regexp.MatchString(matchStr, queryString); !ok {
		return "", 0, 0
	}
	condExprMatch := conditionExpr.FindStringSubmatch(queryString)
	selectExpr := condExprMatch[1]

	matchStr = `(?i)GROUP BY .+(time\(.+\))`
	conditionExpr = regexp.MustCompile(matchStr)
	if ok, _ := regexp.MatchString(matchStr, queryString); !ok {
		minTime = timeRangeArr[0][0]
		maxTime = timeRangeArr[0][1]

		selects := make([]string, 0)
		for i, tag := range tagArr {
			if flagArr[i] == 1 {
				if minTime > timeRangeArr[i][0] {
					minTime = timeRangeArr[i][0]
				}
				if maxTime < timeRangeArr[i][1] {
					maxTime = timeRangeArr[i][1]
				}
				tmpTemplate := queryTemplate
				startTime := TimeInt64ToString(timeRangeArr[i][0])
				endTime := TimeInt64ToString(timeRangeArr[i][1])

				tmpTagString := fmt.Sprintf("\"%s\"='%s'", tag[0], tag[1])

				tmpTemplate = strings.Replace(tmpTemplate, "?", tmpTagString, 1)
				tmpTemplate = strings.Replace(tmpTemplate, "?", startTime, 1)
				tmpTemplate = strings.Replace(tmpTemplate, "?", endTime, 1)
				selects = append(selects, tmpTemplate)
			}

		}

		remainQuerys := strings.Join(selects, ";")

		return remainQuerys, minTime, maxTime
	}
	condExprMatch = conditionExpr.FindStringSubmatch(queryString)
	AggrExpr := condExprMatch[1]

	selects := make([]string, 0)
	for i := 0; i < len(flagArr); i++ {
		if flagArr[i] == 1 {
			if minTime > timeRangeArr[i][0] {
				minTime = timeRangeArr[i][0]
			}
			if maxTime < timeRangeArr[i][1] {
				maxTime = timeRangeArr[i][1]
			}
			tmpCondition := ""
			startTime := TimeInt64ToString(timeRangeArr[i][0])
			endTime := TimeInt64ToString(timeRangeArr[i][1])
			tmpCondition = fmt.Sprintf("%sWHERE (\"%s\"='%s') AND TIME >= '%s' AND TIME < '%s' GROUP BY \"%s\",%s", selectExpr, tagArr[i][0], tagArr[i][1], startTime, endTime, tagName, AggrExpr)

			selects = append(selects, tmpCondition)
		}
	}
	remainQuerys := strings.Join(selects, ";")

	return remainQuerys, minTime, maxTime
}

func InterfaceToByteArray(index int, datatype string, value interface{}) []byte {
	result := make([]byte, 0)

	switch datatype {
	case "bool":
		if value != nil {
			bv, ok := value.(bool)
			if !ok {
				log.Fatal(fmt.Errorf("{}interface fail to convert to bool"))
			} else {
				bBytes, err := BoolToByteArray(bv)
				if err != nil {
					log.Fatal(fmt.Errorf(err.Error()))
				} else {
					result = append(result, bBytes...)
				}
			}
		} else {
			bBytes, _ := BoolToByteArray(false)
			result = append(result, bBytes...)
		}
		break
	case "int64":
		if value != nil {
			if index == 0 {
				if timestamp, ok := value.(string); ok {
					tsi := TimeStringToInt64(timestamp)
					iBytes, err := Int64ToByteArray(tsi)
					if err != nil {
						log.Fatal(fmt.Errorf(err.Error()))
					} else {
						result = append(result, iBytes...)
					}
				} else if timestamp, ok := value.(json.Number); ok {
					jvi, err := timestamp.Int64()
					if err != nil {
						log.Fatal(fmt.Errorf(err.Error()))
					} else {
						iBytes, err := Int64ToByteArray(jvi)
						if err != nil {
							log.Fatal(fmt.Errorf(err.Error()))
						} else {
							result = append(result, iBytes...)
						}
					}
				} else {
					log.Fatal("timestamp fail to convert to []byte")
				}

			} else {
				jv, ok := value.(json.Number)
				if !ok {
					log.Fatal(fmt.Errorf("{}interface fail to convert to json.Number"))
				} else {
					jvi, err := jv.Int64()
					if err != nil {
						log.Fatal(fmt.Errorf(err.Error()))
					} else {
						iBytes, err := Int64ToByteArray(jvi)
						if err != nil {
							log.Fatal(fmt.Errorf(err.Error()))
						} else {
							result = append(result, iBytes...)
						}
					}
				}
			}
		} else {
			iBytes, _ := Int64ToByteArray(0)
			result = append(result, iBytes...)
		}
		break
	case "float64":
		if value != nil {
			jv, ok := value.(json.Number)
			if !ok {
				log.Fatal(fmt.Errorf("{}interface fail to convert to json.Number"))
			} else {
				jvf, err := jv.Float64()
				if err != nil {
					log.Fatal(fmt.Errorf(err.Error()))
				} else {
					fBytes, err := Float64ToByteArray(jvf)
					if err != nil {
						log.Fatal(fmt.Errorf(err.Error()))
					} else {
						result = append(result, fBytes...)
					}
				}
			}
		} else {
			fBytes, _ := Float64ToByteArray(0)
			result = append(result, fBytes...)
		}
		break
	default:
		if value != nil {
			sv, ok := value.(string)
			if !ok {
				log.Fatal(fmt.Errorf("{}interface fail to convert to string"))
			} else {
				sBytes := StringToByteArray(sv)
				result = append(result, sBytes...)
			}
		} else {
			sBytes := StringToByteArray(string(byte(0)))
			result = append(result, sBytes...)
		}
		break
	}
	return result
}

func BytesPerLine(datatypes []string) int {
	bytesPerLine := 0
	for _, d := range datatypes {
		switch d {
		case "bool":
			bytesPerLine += 1
			break
		case "int64":
			bytesPerLine += 8
			break
		case "float64":
			bytesPerLine += 8
			break
		default:
			bytesPerLine += STRINGBYTELENGTH
			break
		}
	}
	return bytesPerLine
}

func BoolToByteArray(b bool) ([]byte, error) {
	bytesBuffer := bytes.NewBuffer([]byte{})
	err := binary.Write(bytesBuffer, binary.LittleEndian, &b)
	if err != nil {
		return nil, err
	}
	return bytesBuffer.Bytes(), nil
}

func ByteArrayToBool(byteArray []byte) (bool, error) {
	if len(byteArray) != 1 {
		return false, errors.New("incorrect length of byte array, can not convert []byte to bool\n")
	}
	var b bool
	byteBuffer := bytes.NewBuffer(byteArray)
	err := binary.Read(byteBuffer, binary.LittleEndian, &b)
	if err != nil {
		return false, err
	}
	return b, nil
}

func StringToByteArray(str string) []byte {
	byteArray := make([]byte, 0, STRINGBYTELENGTH)
	byteStr := []byte(str)
	if len(byteStr) > STRINGBYTELENGTH {
		return byteStr[:STRINGBYTELENGTH]
	}
	byteArray = append(byteArray, byteStr...)
	for i := 0; i < cap(byteArray)-len(byteStr); i++ {
		byteArray = append(byteArray, 0)
	}

	return byteArray
}

func ByteArrayToString(byteArray []byte) string {
	byteArray = bytes.Trim(byteArray, string(byte(0)))
	str := string(byteArray)
	return str
}

func Int64ToByteArray(number int64) ([]byte, error) {
	byteBuffer := bytes.NewBuffer([]byte{})
	err := binary.Write(byteBuffer, binary.LittleEndian, &number)
	if err != nil {
		return nil, err
	}
	return byteBuffer.Bytes(), nil
}

func ByteArrayToInt64(byteArray []byte) (int64, error) {
	if len(byteArray) != 8 {
		return 0, errors.New("incorrect length of byte array, can not convert []byte to int64\n")
	}
	var number int64
	byteBuffer := bytes.NewBuffer(byteArray)
	err := binary.Read(byteBuffer, binary.LittleEndian, &number)
	if err != nil {
		return 0, err
	}
	return number, nil
}

func Float64ToByteArray(number float64) ([]byte, error) {
	byteBuffer := bytes.NewBuffer([]byte{})
	err := binary.Write(byteBuffer, binary.LittleEndian, &number)
	if err != nil {
		return nil, err
	}
	return byteBuffer.Bytes(), nil
}

func ByteArrayToFloat64(byteArray []byte) (float64, error) {
	if len(byteArray) != 8 {
		return 0, errors.New("incorrect length of byte array, can not canvert []byte to float64\n")
	}
	var number float64
	byteBuffer := bytes.NewBuffer(byteArray)
	err := binary.Read(byteBuffer, binary.LittleEndian, &number)
	if err != nil {
		return 0.0, err
	}
	return number, nil
}

func TimeStringToInt64(timestamp string) int64 {
	timeT, _ := time.Parse(time.RFC3339, timestamp)
	numberN := timeT.Unix()

	return numberN
}

func TimeInt64ToString(number int64) string {
	t := time.Unix(number, 0).UTC()
	timestamp := t.Format(time.RFC3339)

	return timestamp
}

func NanoTimeInt64ToString(number int64) string {
	t := time.Unix(0, number).UTC()
	timestamp := t.Format(time.RFC3339)

	return timestamp
}
