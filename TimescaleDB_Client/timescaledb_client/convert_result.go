package timescaledb_client

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"time"
)

func ByteArrayToResponseWithDatatype(byteArray []byte, datatypes []string) ([][][]interface{}, int, []uint8, [][]int64, [][]string) {

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
		sttIdx := strings.Index(curSeg, "=")
		endIdx := strings.Index(curSeg, ")")
		tag := curSeg[sttIdx+1 : endIdx]
		values = nil
		for len(values) < lines {
			value = nil
			for i, d := range datatypes {
				if i == 1 {
					value = append(value, tag)
					continue
				}
				switch d {
				case "string":
					bStartIdx := index
					index += STRINGBYTELENGTH
					bEndIdx := index
					tmp := ByteArrayToString(byteArray[bStartIdx:bEndIdx])
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
					str := TimeInt64ToString(tmp)
					tm, err := time.Parse(goTimeFmt, str)
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tm)
					break
				case "float64":
					fStartIdx := index
					index += 8
					fEndIdx := index
					tmp, err := ByteArrayToFloat64(byteArray[fStartIdx:fEndIdx])
					if err != nil {
						log.Fatal(err)
					}
					value = append(value, tmp)
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

	for _, s := range seprateSemanticSegments {
		messages := strings.Split(s, "#")
		ssm := messages[0][2 : len(messages[0])-2]
		merged := strings.Split(ssm, ",")
		nameIndex := strings.Index(merged[0], ".")
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

			tmpTagArr := make([]string, 2)
			tmpTagArr[0] = key
			tmpTagArr[1] = val
			tagArr = append(tagArr, tmpTagArr)
		}
	}

	return valuess, flagNum, flagArr, timeRangeArr, tagArr
}

func ResponseToByteArrayWithParams(resp *sql.Rows, datatypes []string, tags []string, metric string, partialSegment string) ([]byte, int64) {
	result := make([]byte, 0)
	dataBytes := make([]byte, 0)

	if ResponseIsEmpty(resp) {
		return nil, 0
	}

	singleSegments := GetSingleSegment(metric, partialSegment, tags)

	bytesPerLine := BytesPerLine(datatypes)

	respData := RowsToInterface(resp, len(datatypes))
	if len(respData) == 0 {
		for _, ss := range singleSegments {
			emptyByteArray := EmptyResultByteArray(ss)
			result = append(result, emptyByteArray...)
		}
		return result, int64(len(singleSegments))
	}

	curTag := respData[0][1].(string)
	var curLines int = 0
	segIdx := 0
	for _, row := range respData {

		tag := row[1].(string)
		if tag != curTag {
			for segIdx < len(tags) {
				tidx := strings.Index(tags[segIdx], "=")
				segTag := tags[segIdx][tidx+1:]
				if segTag != curTag {
					emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
					result = append(result, emptyByteArray...)
					segIdx++
				} else {
					break
				}
			}

			curTag = tag
			bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * curLines))
			curLines = 0
			result = append(result, []byte(singleSegments[segIdx])...)
			result = append(result, []byte(" ")...)
			result = append(result, bytesPerSeries...)
			result = append(result, dataBytes...)

			dataBytes = nil
			segIdx++
		}
		curLines++
		for j, v := range row {
			if j == 1 {
				continue
			}
			datatype := datatypes[j]
			tmpBytes := InterfaceToByteArray(j, datatype, v)
			dataBytes = append(dataBytes, tmpBytes...)
		}

	}

	for segIdx < len(tags) {
		tidx := strings.Index(tags[segIdx], "=")
		segTag := tags[segIdx][tidx+1:]
		if segTag != curTag {
			emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
			result = append(result, emptyByteArray...)
			segIdx++
		} else {
			break
		}
	}

	bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * curLines))
	result = append(result, []byte(singleSegments[segIdx])...)
	result = append(result, []byte(" ")...)
	result = append(result, bytesPerSeries...)
	result = append(result, dataBytes...)
	segIdx++

	for segIdx < len(tags) {
		tidx := strings.Index(tags[segIdx], "=")
		segTag := tags[segIdx][tidx+1:]
		if segTag != curTag {
			emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
			result = append(result, emptyByteArray...)
			segIdx++
		} else {
			break
		}
	}

	return result, int64(segIdx)
}

func ResponseInterfaceToByteArrayWithParams(respData [][]interface{}, datatypes []string, tags []string, metric string, partialSegment string) ([]byte, int64) {
	var numberOfTable int64 = 0
	result := make([]byte, 0)
	dataBytes := make([]byte, 0)

	singleSegments := GetSingleSegment(metric, partialSegment, tags)

	if len(respData) == 0 {
		for _, ss := range singleSegments {
			emptyByteArray := EmptyResultByteArray(ss)
			result = append(result, emptyByteArray...)
		}
		return result, int64(len(singleSegments))
	}

	bytesPerLine := BytesPerLine(datatypes)

	curTag := respData[0][1].(string)
	var curLines int = 0
	segIdx := 0
	for _, row := range respData {

		tag := row[1].(string)
		if tag != curTag {
			for segIdx < len(tags) {
				tidx := strings.Index(tags[segIdx], "=")
				segTag := tags[segIdx][tidx+1:]
				if segTag != curTag {
					emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
					result = append(result, emptyByteArray...)
					numberOfTable++
					segIdx++
				} else {
					break
				}
			}

			curTag = tag
			bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * curLines))
			curLines = 0
			result = append(result, []byte(singleSegments[segIdx])...)
			result = append(result, []byte(" ")...)
			result = append(result, bytesPerSeries...)
			result = append(result, dataBytes...)

			dataBytes = nil
			numberOfTable++
			segIdx++
		}
		curLines++
		for j, v := range row {
			if j == 1 {
				continue
			}
			datatype := datatypes[j]
			tmpBytes := InterfaceToByteArray(j, datatype, v)
			dataBytes = append(dataBytes, tmpBytes...)
		}

	}

	for segIdx < len(tags) {
		tidx := strings.Index(tags[segIdx], "=")
		segTag := tags[segIdx][tidx+1:]
		if segTag != curTag {
			emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
			result = append(result, emptyByteArray...)
			numberOfTable++
			segIdx++
		} else {
			break
		}
	}
	bytesPerSeries, _ := Int64ToByteArray(int64(bytesPerLine * curLines))
	result = append(result, []byte(singleSegments[segIdx])...)
	result = append(result, []byte(" ")...)
	result = append(result, bytesPerSeries...)
	result = append(result, dataBytes...)
	numberOfTable++
	segIdx++

	for segIdx < len(tags) {
		tidx := strings.Index(tags[segIdx], "=")
		segTag := tags[segIdx][tidx+1:]
		if segTag != curTag {
			emptyByteArray := EmptyResultByteArray(singleSegments[segIdx])
			result = append(result, emptyByteArray...)
			numberOfTable++
			segIdx++
		} else {
			break
		}
	}

	return result, numberOfTable
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
			if reflect.ValueOf(value).CanInt() {
				tmpInt := reflect.ValueOf(value).Int()
				iBytes, err := Int64ToByteArray(tmpInt)
				if err != nil {
					log.Fatal(fmt.Errorf(err.Error()))
				} else {
					result = append(result, iBytes...)
				}
			} else if tm, ok := value.(time.Time); ok {
				str := tm.Format(goTimeFmt)
				tmpInt := TimeStringToInt64(str)
				iBytes, err := Int64ToByteArray(tmpInt)
				if err != nil {
					log.Fatal(fmt.Errorf(err.Error()))
				} else {
					result = append(result, iBytes...)
				}
			} else {
				log.Fatal(fmt.Errorf("{}interface fail to convert to int64"))
			}
		} else {
			iBytes, _ := Int64ToByteArray(0)
			result = append(result, iBytes...)
		}
		break
	case "float64":
		if value != nil {
			if reflect.ValueOf(value).CanFloat() {
				tmpFloat := reflect.ValueOf(value).Float()
				fBytes, err := Float64ToByteArray(tmpFloat)
				if err != nil {
					log.Fatal(fmt.Errorf(err.Error()))
				} else {
					result = append(result, fBytes...)
				}
			} else {
				log.Fatal(fmt.Errorf("{}interface fail to convert to float64"))
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
	for i, d := range datatypes {
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
		case "string":
			if i != 1 {
				bytesPerLine += STRINGBYTELENGTH
			}
			break
		default:
			bytesPerLine += 8
			break
		}
	}
	return bytesPerLine
}

func ColumnLength(datatypes []string) []int64 {
	bytesPerLine := make([]int64, 0)
	for _, d := range datatypes {
		switch d {
		case "bool":
			bytesPerLine = append(bytesPerLine, 1)
			break
		case "int64":
			bytesPerLine = append(bytesPerLine, 8)
			break
		case "float64":
			bytesPerLine = append(bytesPerLine, 8)
			break
		case "string":
			bytesPerLine = append(bytesPerLine, STRINGBYTELENGTH)
			break
		default:
			bytesPerLine = append(bytesPerLine, 8)
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

func ResultToString(rows *sql.Rows) string {
	var result string

	if ResponseIsEmpty(rows) {
		return "empty response"
	}

	col, _ := rows.Columns()
	dataArray := RowsToInterface(rows, len(col))
	colTypes := DataTypeFromColumn(len(col))
	for i, colName := range col {
		result += fmt.Sprintf("%s[%s]\t", colName, colTypes[i])
	}
	result += "\n"

	for _, row := range dataArray {
		for j, col := range row {
			if col == nil {
				result += "-"
			} else if colTypes[j] == "string" {
				str := col.(string)
				result += str
			} else if reflect.ValueOf(col).CanInt() {
				tmpInt := reflect.ValueOf(col).Int()
				str := strconv.FormatInt(tmpInt, 10)
				result += str
			} else if reflect.ValueOf(col).CanFloat() {
				tmpFloat := reflect.ValueOf(col).Float()
				str := strconv.FormatFloat(tmpFloat, 'g', -1, 64)
				result += str
			} else if colTypes[j] == "bool" {
				str := ""
				if reflect.ValueOf(col).Bool() {
					str = "1"
				} else {
					str = "0"
				}
				result += str
			} else {
				tm := col.(time.Time)
				str := tm.Format(goTimeFmt)
				result += str
			}
			result += "\t"
		}

		result += "\n"
	}

	result += "end"
	return result
}

func ResultInterfaceToString(dataArray [][]interface{}, colTypes []string) string {
	var result string

	if len(dataArray) == 0 {
		return "empty response"
	}

	for _, row := range dataArray {
		for j, col := range row {
			if col == nil {
				result += "-"
			} else if colTypes[j] == "string" {
				str := col.(string)
				result += str
			} else if reflect.ValueOf(col).CanInt() {
				tmpInt := reflect.ValueOf(col).Int()
				str := strconv.FormatInt(tmpInt, 10)
				result += str
			} else if reflect.ValueOf(col).CanFloat() {
				tmpFloat := reflect.ValueOf(col).Float()
				str := strconv.FormatFloat(tmpFloat, 'g', -1, 64)
				result += str
			} else if colTypes[j] == "bool" {
				str := ""
				if reflect.ValueOf(col).Bool() {
					str = "1"
				} else {
					str = "0"
				}
				result += str
			} else {
				tm := col.(time.Time)
				str := tm.Format(goTimeFmt)
				result += str
			}
			result += "\t"
		}

		result += "\n"
	}

	result += "end"
	return result
}
