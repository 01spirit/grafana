package influxdb_client

import (
	"encoding/json"
	"fmt"
	"github.com/grafana/grafana/InfluxDB-client/models"
	"log"
	"slices"
	"strings"
	"time"
)

type Series struct {
	Name    string            // measurement name
	Tags    map[string]string // GROUP BY tags
	Columns []string          // column name
	Values  [][]interface{}   // specific query results
	Partial bool              // useless (false)
}

type RespWithTimeRange struct {
	resp      *Response
	startTime int64
	endTime   int64
}

func Merge(precision string, resps ...*Response) []*Response {
	var results []*Response
	var resp1 *Response
	var resp2 *Response
	var respTmp *Response

	if len(resps) <= 1 {
		return resps
	}

	duration, err := time.ParseDuration(precision)
	if err != nil {
		log.Fatalln(err)
	}
	timeRange := int64(duration.Seconds())

	resps = SortResponses(resps)
	if len(resps) <= 1 {
		return resps
	}

	index := 0
	merged := false
	results = append(results, resps[0])
	for _, resp := range resps[1:] {
		resp1 = results[index]
		resp2 = resp

		st1, et1 := GetResponseTimeRange(resp1)
		st2, et2 := GetResponseTimeRange(resp2)

		if et1 <= st2 {
			if st2-et1 <= timeRange {
				respTmp = MergeResultTable(resp1, resp2)
				merged = true
				results[index] = respTmp
			}
		} else if et2 <= st1 {
			if st1-et2 <= timeRange {
				respTmp = MergeResultTable(resp2, resp1)
				merged = true
				results[index] = respTmp
			}
		} else {
			if st1 < st2 && et1 > et2 {
				respTmp = MergeContainedResultTable(resp1, resp2)
				merged = true
				results[index] = respTmp
			} else if st1 > st2 && et1 < et2 {
				respTmp = MergeContainedResultTable(resp2, resp1)
				merged = true
				results[index] = respTmp
			}
		}

		if !merged {
			results = append(results, resp2)
			index++
		}

		merged = false

	}

	return results
}

func MergeRemainResponse(remainResp, convResp *Response) *Response {

	if ResponseIsEmpty(remainResp) {
		return convResp
	}
	if ResponseIsEmpty(convResp) {
		return remainResp
	}

	seriesArr := make([]models.Row, 0)

	index1 := 0
	index2 := 0
	for index1 < len(remainResp.Results) && index2 < len(convResp.Results[0].Series) {
		if remainResp.Results[index1].Series == nil {
			seriesArr = append(seriesArr, convResp.Results[0].Series[index2])
			index1++
			index2++
		} else if convResp.Results[0].Series[index2].Values == nil {
			seriesArr = append(seriesArr, remainResp.Results[index1].Series[0])
			index1++
			index2++
		} else {
			tag1 := TagsMapToString(remainResp.Results[index1].Series[0].Tags)
			tag2 := TagsMapToString(convResp.Results[0].Series[index2].Tags)

			cmp := strings.Compare(tag1, tag2)
			if cmp == -1 {
				seriesArr = append(seriesArr, remainResp.Results[index1].Series[0])
				index1++
			} else if cmp == 1 {
				seriesArr = append(seriesArr, convResp.Results[0].Series[index2])
				index2++
			} else {
				seriesArr = append(seriesArr, mergeSeries(remainResp.Results[index1].Series[0], convResp.Results[0].Series[index2]))
				index1++
				index2++
			}
		}

	}

	for index1 < len(remainResp.Results) {
		seriesArr = append(seriesArr, remainResp.Results[index1].Series[0])
		index1++
	}

	for index2 < len(convResp.Results[0].Series) {
		seriesArr = append(seriesArr, convResp.Results[0].Series[index2])
		index2++
	}

	convResp.Results[0].Series = seriesArr

	return convResp
}

func MergeResponse(resp1, resp2 *Response) *Response {
	if ResponseIsEmpty(resp1) {
		return resp2
	}
	if ResponseIsEmpty(resp2) {
		return resp1
	}

	seriesArr := make([]models.Row, 0)
	index1 := 0
	index2 := 0
	for index1 < len(resp1.Results[0].Series) && index2 < len(resp2.Results[0].Series) {

		tag1 := TagsMapToString(resp1.Results[0].Series[index1].Tags)
		tag2 := TagsMapToString(resp2.Results[0].Series[index2].Tags)

		cmp := strings.Compare(tag1, tag2)
		if cmp == -1 {
			seriesArr = append(seriesArr, resp1.Results[0].Series[index1])
			index1++
		} else if cmp == 1 {
			seriesArr = append(seriesArr, resp2.Results[0].Series[index2])
			index2++
		} else {
			seriesArr = append(seriesArr, mergeSeries(resp1.Results[0].Series[index1], resp2.Results[0].Series[index2]))
			index1++
			index2++
		}
	}

	for index1 < len(resp1.Results[0].Series) {
		seriesArr = append(seriesArr, resp1.Results[0].Series[index1])
		index1++
	}

	for index2 < len(resp2.Results[0].Series) {
		seriesArr = append(seriesArr, resp2.Results[0].Series[index2])
		index2++
	}

	resp1.Results[0].Series = seriesArr

	return resp1
}

func mergeSeries(series1, series2 models.Row) models.Row {
	st1, et1, err1 := GetSeriesTimeRange(series1)
	if err1 != nil {
		return series2
	}
	st2, et2, err2 := GetSeriesTimeRange(series2)
	if err2 != nil {
		return series1
	}

	if st2 > et1 {
		series1.Values = append(series1.Values, series2.Values...)
		return series1
	} else if st1 > et2 {
		series2.Values = append(series2.Values, series1.Values...)
		return series2
	} else if st1 < st2 {
		tmpSeries := models.Row{
			Name:    series1.Name,
			Tags:    series1.Tags,
			Columns: series1.Columns,
			Values:  make([][]interface{}, 0),
			Partial: false,
		}
		insPos := SearchInsertPosition(series1.Values, series2.Values)

		for i := 0; i < insPos; i++ {
			tmpSeries.Values = append(tmpSeries.Values, series1.Values[i])
		}

		for i := 0; i < len(series2.Values); i++ {
			tmpSeries.Values = append(tmpSeries.Values, series2.Values[i])
		}

		for i := insPos; i < len(series1.Values); i++ {
			tmpSeries.Values = append(tmpSeries.Values, series1.Values[i])
		}
		return tmpSeries
	} else {
		tmpSeries := models.Row{
			Name:    series1.Name,
			Tags:    series1.Tags,
			Columns: series1.Columns,
			Values:  make([][]interface{}, 0),
			Partial: false,
		}
		insPos := SearchInsertPosition(series2.Values, series1.Values)

		for i := 0; i < insPos; i++ {
			tmpSeries.Values = append(tmpSeries.Values, series2.Values[i])
		}

		for i := 0; i < len(series1.Values); i++ {
			tmpSeries.Values = append(tmpSeries.Values, series1.Values[i])
		}

		for i := insPos; i < len(series2.Values); i++ {
			tmpSeries.Values = append(tmpSeries.Values, series2.Values[i])
		}
		return tmpSeries
	}

}

func SortResponses(resps []*Response) []*Response {
	var results []*Response
	respArrTmp := make([]RespWithTimeRange, 0)

	for _, resp := range resps {
		if !ResponseIsEmpty(resp) {
			st, et := GetResponseTimeRange(resp)
			rwtr := RespWithTimeRange{resp, st, et}
			respArrTmp = append(respArrTmp, rwtr)
		}
	}

	respArrTmp = SortResponseWithTimeRange(respArrTmp)
	for _, rt := range respArrTmp {
		results = append(results, rt.resp)
	}

	return results
}

func SortResponseWithTimeRange(rwtr []RespWithTimeRange) []RespWithTimeRange {
	n := len(rwtr)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if rwtr[j].startTime >= rwtr[j+1].endTime {
				rwtr[j], rwtr[j+1] = rwtr[j+1], rwtr[j]
			}
		}
	}
	return rwtr
}

func SeriesToRow(ser Series) models.Row {
	return models.Row{
		Name:    ser.Name,
		Tags:    ser.Tags,
		Columns: ser.Columns,
		Values:  ser.Values,
		Partial: ser.Partial,
	}
}

func GetSeriesTagsMap(resp *Response) map[int]map[string]string {
	tagsMap := make(map[int]map[string]string)

	if !ResponseIsEmpty(resp) {
		for i, ser := range resp.Results[0].Series {
			tagsMap[i] = ser.Tags
		}
	}

	return tagsMap
}

func TagsMapToString(tagsMap map[string]string) string {
	var str string
	tagKeyArr := make([]string, 0)

	if len(tagsMap) == 0 {
		return ""
	}
	for k, _ := range tagsMap {
		tagKeyArr = append(tagKeyArr, k)
	}
	slices.Sort(tagKeyArr)

	for _, s := range tagKeyArr {
		str += fmt.Sprintf("%s=%s ", s, tagsMap[s])
	}

	return str
}

func MergeSeries(resp1, resp2 *Response) []Series {
	resSeries := make([]Series, 0)
	tagsMapMerged := make(map[int]map[string]string)

	tagsMap1 := GetSeriesTagsMap(resp1)
	tagsMap2 := GetSeriesTagsMap(resp2)

	resTagArr := make([]string, 0)
	if len(tagsMap1[0]) <= len(tagsMap2[0]) {
		for k := range tagsMap1[0] {
			resTagArr = append(resTagArr, k)
		}
	} else {
		for k := range tagsMap2[0] {
			resTagArr = append(resTagArr, k)
		}
	}

	len1 := len(tagsMap1)
	len2 := len(tagsMap2)

	var str1 string
	var str2 string
	for _, v := range tagsMap1 {
		str1 += TagsMapToString(v)
		str1 += ";"
	}
	for _, v := range tagsMap2 {
		str2 += TagsMapToString(v)
		str2 += ";"
	}

	index1 := 0
	index2 := 0
	indexAll := 0
	same := false
	for index1 < len1 || index2 < len2 {
		if index1 < len1 && index2 < len2 {
			same = true

			tagStr1 := TagsMapToString(tagsMap1[index1])
			tagStr2 := TagsMapToString(tagsMap2[index2])

			if !strings.Contains(tagStr1, tagStr2) && !strings.Contains(tagStr2, tagStr1) {
				if !strings.Contains(str2, tagStr1) {
					tmpMap := make(map[string]string)
					for k, v := range tagsMap1[index1] {
						if slices.Contains(resTagArr, k) {
							tmpMap[k] = v
						}
					}
					tagsMapMerged[indexAll] = tmpMap
					index1++
					indexAll++
				} else if !strings.Contains(str1, tagStr2) {
					tmpMap := make(map[string]string)
					for k, v := range tagsMap2[index2] {
						if slices.Contains(resTagArr, k) {
							tmpMap[k] = v
						}
					}
					tagsMapMerged[indexAll] = tmpMap
					index2++
					indexAll++
				}
				same = false
			}

			if same {
				if strings.Contains(tagStr1, tagStr2) {
					tagsMapMerged[indexAll] = tagsMap2[index2]
					index1++
					index2++
					indexAll++
				} else {
					tagsMapMerged[indexAll] = tagsMap1[index1]
					index1++
					index2++
					indexAll++
				}
			}

		} else if index1 == len1 && index2 < len2 {
			tmpMap := make(map[string]string)
			for k, v := range tagsMap2[index2] {
				if slices.Contains(resTagArr, k) {
					tmpMap[k] = v
				}
			}
			tagsMapMerged[indexAll] = tmpMap
			index2++
			indexAll++
		} else if index1 < len1 && index2 == len2 {
			tmpMap := make(map[string]string)
			for k, v := range tagsMap1[index1] {
				if slices.Contains(resTagArr, k) {
					tmpMap[k] = v
				}
			}
			tagsMapMerged[indexAll] = tmpMap
			index1++
			indexAll++
		}
	}

	var tagsStrArr []string
	for i := 0; i < indexAll; i++ {
		tmpSeries := Series{
			Name:    resp1.Results[0].Series[0].Name,
			Tags:    tagsMapMerged[i],
			Columns: resp1.Results[0].Series[0].Columns,
			Values:  make([][]interface{}, 0),
			Partial: resp1.Results[0].Series[0].Partial,
		}
		resSeries = append(resSeries, tmpSeries)
		tagsStrArr = append(tagsStrArr, TagsMapToString(tmpSeries.Tags))
	}
	slices.Sort(tagsStrArr)

	sortedSeries := make([]Series, 0)
	for i := range tagsStrArr {
		for j := range resSeries {
			s := TagsMapToString(resSeries[j].Tags)
			if strings.Compare(s, tagsStrArr[i]) == 0 {
				sortedSeries = append(sortedSeries, resSeries[j])
				break
			}
		}
	}

	return sortedSeries
}

func MergeResultTable(resp1, resp2 *Response) *Response {
	respRow := make([]models.Row, 0)

	mergedSeries := MergeSeries(resp1, resp2)

	len1 := len(resp1.Results[0].Series)
	len2 := len(resp2.Results[0].Series)

	index1 := 0
	index2 := 0
	for _, ser := range mergedSeries {
		if index1 < len1 && strings.Compare(TagsMapToString(resp1.Results[0].Series[index1].Tags), TagsMapToString(ser.Tags)) == 0 {
			ser.Values = append(ser.Values, resp1.Results[0].Series[index1].Values...)
			index1++
		}
		if index2 < len2 && strings.Compare(TagsMapToString(resp2.Results[0].Series[index2].Tags), TagsMapToString(ser.Tags)) == 0 {
			ser.Values = append(ser.Values, resp2.Results[0].Series[index2].Values...)
			index2++
		}
		respRow = append(respRow, SeriesToRow(ser))
	}

	resp1.Results[0].Series = respRow

	return resp1
}

func MergeContainedResultTable(resp1, resp2 *Response) *Response {
	respRow := make([]models.Row, 0)

	mergedSeries := MergeSeries(resp1, resp2)

	len1 := len(resp1.Results[0].Series)
	len2 := len(resp2.Results[0].Series)

	index1 := 0
	index2 := 0

	for _, ser := range mergedSeries {

		if index1 < len1 && strings.Compare(TagsMapToString(resp1.Results[0].Series[index1].Tags), TagsMapToString(ser.Tags)) == 0 && index2 < len2 && strings.Compare(TagsMapToString(resp2.Results[0].Series[index2].Tags), TagsMapToString(ser.Tags)) == 0 {

			insPos := SearchInsertPosition(resp1.Results[0].Series[index1].Values, resp2.Results[0].Series[index2].Values)

			for i := 0; i < insPos; i++ {
				ser.Values = append(ser.Values, resp1.Results[0].Series[index1].Values[i])
			}
			for i := 0; i < len(resp2.Results[0].Series[index2].Values); i++ {
				ser.Values = append(ser.Values, resp2.Results[0].Series[index2].Values[i])
			}
			for i := insPos; i < len(resp1.Results[0].Series[index1].Values); i++ {
				ser.Values = append(ser.Values, resp1.Results[0].Series[index1].Values[i])
			}

			index1++
			index2++
		} else if index1 < len1 && strings.Compare(TagsMapToString(resp1.Results[0].Series[index1].Tags), TagsMapToString(ser.Tags)) == 0 {

			ser.Values = append(ser.Values, resp1.Results[0].Series[index1].Values...)
			index1++
		} else if index2 < len2 && strings.Compare(TagsMapToString(resp2.Results[0].Series[index2].Tags), TagsMapToString(ser.Tags)) == 0 {

			ser.Values = append(ser.Values, resp2.Results[0].Series[index2].Values...)
			index2++
		}

		respRow = append(respRow, SeriesToRow(ser))
	}

	resp1.Results[0].Series = respRow

	return resp1
}

func SearchInsertPosition(values1, values2 [][]interface{}) int {
	index := 0

	if len(values2) == 0 || len(values1) == 0 {
		return 0
	}
	timestamp, ok := values2[0][0].(json.Number)
	if !ok {
		log.Fatal(fmt.Errorf("search insert position fail during merge resp"))
	}
	st2, err := timestamp.Int64()
	if err != nil {
		log.Fatal(fmt.Errorf(err.Error()))
	}

	left := 0
	right := len(values1) - 1
	for left <= right {
		mid := (left + right) / 2

		tmstmp, ok := values1[mid][0].(json.Number)
		if !ok {
			log.Fatal(fmt.Errorf("search insert position fail during merge resp"))
		}
		st1, err := tmstmp.Int64()
		if err != nil {
			log.Fatal(fmt.Errorf(err.Error()))
		}

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
