package timescaledb_client

import (
	"fmt"
	"math"
	"strings"
	"time"
)

func MergeRemainResponse(convResp [][][]interface{}, remainResp [][]interface{}, timeRangeArr [][]int64) [][][]interface{} {
	if len(convResp) == 0 {
		return [][][]interface{}{remainResp}
	}

	if len(remainResp) == 0 {
		return convResp
	}

	var results [][][]interface{}

	index1 := 0
	index2 := 0
	for index1 < len(convResp) && index2 < len(remainResp) {
		if len(convResp) == 0 {
			index1++
			continue
		}
		if len(convResp[index1]) == 0 {
			index1++
			continue
		}
		tag1 := convResp[index1][0][1].(string)
		tag2 := remainResp[index2][1].(string)

		cmp := strings.Compare(tag1, tag2)

		if cmp == -1 {
			results = append(results, convResp[index1])
			index1++
		} else if cmp == 1 {
			var data [][]interface{}
			for index2 < len(remainResp) {
				tagT := remainResp[index2][1].(string)
				if tagT == tag2 {
					data = append(data, remainResp[index2])
					index2++
				} else {
					break
				}
			}

			results = append(results, data)
		} else {
			data, offset := mergeResp(convResp[index1], remainResp[index2:], timeRangeArr[index1])

			results = append(results, data)
			index1++
			index2 += offset
		}

	}

	for index1 < len(convResp) {
		results = append(results, convResp[index1])
		index1++
	}

	for index2 < len(remainResp) {
		var data [][]interface{}
		tag2 := remainResp[index2][1].(string)
		for index2 < len(remainResp) {
			tagT := remainResp[index2][1].(string)
			if tagT == tag2 {
				data = append(data, remainResp[index2])
				index2++
			} else {
				break
			}
		}

		results = append(results, data)
	}

	return results
}

func mergeResp(series1, series2 [][]interface{}, timeRange []int64) ([][]interface{}, int) {
	series2 = searchInsertValue(series2, timeRange)

	st1, et1, err1 := GetSeriesTimeRange(series1)
	if err1 != nil {
		return series2, len(series2)
	}

	st2 := timeRange[0]
	et2 := timeRange[1]

	index := 0
	if st2 > et1 {
		tag1 := series1[0][1].(string)
		for index < len(series2) {
			tag2 := series2[index][1].(string)
			if tag1 == tag2 {
				series1 = append(series1, series2[index])
				index++
			} else {
				break
			}
		}

		return series1, index
	} else if st1 > et2 {
		tag1 := series1[0][1].(string)
		tmpSer := make([][]interface{}, 0)
		for index < len(series2) {
			tag2 := series2[index][1].(string)
			if tag1 == tag2 {
				tmpSer = append(tmpSer, series2[index])
				index++
			} else {
				break
			}
		}
		tmpSer = append(tmpSer, series1...)

		return tmpSer, index
	} else if st1 < st2 {
		tmpSeries := make([][]interface{}, 0)
		insPos := searchInsertPosition(series1, series2)
		for i := 0; i < insPos; i++ {
			tmpSeries = append(tmpSeries, series1[i])
		}
		tag1 := series1[0][1].(string)
		for i := 0; i < len(series2); i++ {
			tag2 := series2[i][1].(string)
			if tag1 == tag2 {
				tmpSeries = append(tmpSeries, series2[i])
				index++
			} else {
				break
			}
		}
		for i := insPos; i < len(series1); i++ {
			tmpSeries = append(tmpSeries, series1[i])
		}
		return tmpSeries, index
	} else {
		tmpSeries := make([][]interface{}, 0)
		insPos := searchInsertPosition(series2, series1)
		tag1 := series1[0][1].(string)
		for i := 0; i < insPos; i++ {
			tag2 := series2[i][1].(string)
			if tag1 == tag2 {
				tmpSeries = append(tmpSeries, series2[i])
				index++
			} else {
				break
			}
		}
		for i := 0; i < len(series1); i++ {
			tmpSeries = append(tmpSeries, series1[i])
		}
		for i := insPos; i < len(series2); i++ {
			tag2 := series2[i][1].(string)
			if tag1 == tag2 {
				tmpSeries = append(tmpSeries, series2[i])
				index++
			} else {
				break
			}
		}
		return tmpSeries, index
	}

}

func GetSeriesTimeRange(series [][]interface{}) (int64, int64, error) {
	if len(series) == 0 {
		return 0, 0, fmt.Errorf("empty series")
	}

	stime := series[0][0].(time.Time)
	etime := series[len(series)-1][0].(time.Time)

	return stime.Unix(), etime.Unix(), nil
}

func searchInsertPosition(values1, values2 [][]interface{}) int {
	index := 0

	if len(values2) == 0 || len(values1) == 0 {
		return 0
	}

	st2 := values2[0][0].(time.Time).Unix()

	left := 0
	right := len(values1) - 1
	for left < right {
		mid := (left + right) / 2

		st1 := values1[mid][0].(time.Time).Unix()

		if st1 < st2 {
			index = mid
			left = mid + 1
		} else if st1 > st2 {
			index = mid
			right = mid - 1
		} else {
			index = mid
			return index
		}
	}

	return index
}

func RemainQuery(queryTemplate string, flagArr []uint8, timeRangeArr [][]int64, tagArr [][]string) (string, int64, int64) {
	if len(flagArr) == 0 || len(timeRangeArr) == 0 {
		return "", 0, 0
	}

	var maxTime int64 = 0
	var minTime int64 = math.MaxInt64

	predicate := ""
	num := strings.Count(queryTemplate, "AND")
	if num > 2 {
		aIdx := strings.Index(queryTemplate, "AND time < '?'")
		oIdx := strings.Index(queryTemplate, "ORDER")
		predicate += " "
		predicate += queryTemplate[aIdx+len("AND time < '?'")+1 : oIdx-1]
		queryTemplate = strings.Replace(queryTemplate, predicate, "", -1)
	}

	queries := make([]string, 0)
	tags := make([]string, 0)
	for i := 0; i < len(flagArr); i++ {
		if flagArr[i] == 1 {
			if minTime > timeRangeArr[i][0] {
				minTime = timeRangeArr[i][0]
			}
			if maxTime < timeRangeArr[i][1] {
				maxTime = timeRangeArr[i][1]
			}

			startTime := TimeInt64ToString(timeRangeArr[i][0])
			endTime := TimeInt64ToString(timeRangeArr[i][1])

			splitQuery := fmt.Sprintf("(%s='%s' AND time >= '%s' AND time < '%s'%s)",
				tagArr[i][0], tagArr[i][1], startTime, endTime, predicate)
			queries = append(queries, splitQuery)

			splitTags := fmt.Sprintf("'%s'", tagArr[i][1])
			tags = append(tags, splitTags)
		}
	}

	qstr := fmt.Sprintf("time >= '%s' AND time < '%s' AND %s IN (%s) AND (%s)",
		TimeInt64ToString(minTime), TimeInt64ToString(maxTime), tagArr[0][0], strings.Join(tags, ","), strings.Join(queries, " OR "))

	queryTemplate = strings.Replace(queryTemplate, " AND time >= '?'", "", -1)
	queryTemplate = strings.Replace(queryTemplate, " AND time < '?'", "", -1)

	queryTemplate = strings.Replace(queryTemplate, "?", qstr, 1)

	return queryTemplate, minTime, maxTime
}

func searchInsertValue(values [][]interface{}, timeRange []int64) [][]interface{} {
	if len(values) == 0 || timeRange[0] == 0 || timeRange[1] == 0 {
		return nil
	}

	startTime := timeRange[0]
	endTime := timeRange[1]

	sIdx := 0
	left := 0
	right := len(values) - 1
	for left < right {
		mid := (left + right) / 2

		st1 := values[mid][0].(time.Time).Unix()

		if st1 < startTime {
			sIdx = mid
			left = mid + 1
		} else if st1 > startTime {
			sIdx = mid
			right = mid - 1
		} else {
			sIdx = mid
			break
		}
	}

	eIdx := len(values) - 1
	left = 0
	right = len(values) - 1
	for left < right {
		mid := (left + right) / 2

		et1 := values[mid][0].(time.Time).Unix()

		if et1 > endTime {
			eIdx = mid
			right = mid - 1
		} else if et1 < endTime {
			eIdx = mid
			left = mid + 1
		} else {
			eIdx = mid
			break
		}
	}

	if sIdx > eIdx {
		return nil
	}

	return values[sIdx : eIdx+1]
}
