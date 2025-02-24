package influxdb_client

import (
	"fmt"
	"github.com/influxdata/influxql"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

func GetInterval(queryString string) string {

	matchStr := `(?i).+GROUP BY (.+)`
	conditionExpr := regexp.MustCompile(matchStr)
	if ok, _ := regexp.MatchString(matchStr, queryString); !ok {
		return "empty"
	}
	condExprMatch := conditionExpr.FindStringSubmatch(queryString)
	parseExpr := condExprMatch[1]

	groupby := strings.Split(parseExpr, ",")
	for _, tag := range groupby {
		if strings.Contains(tag, "time") {
			tag = strings.TrimSpace(tag)
			startIndex := strings.Index(tag, "(") + 1
			endIndex := strings.Index(tag, ")")

			interval := tag[startIndex:endIndex]

			return interval
		}
	}

	return "empty"
}

func GroupByTags(queryString string, measurementName string) []string {
	matchStr := `(?i).+GROUP BY (.+)`
	conditionExpr := regexp.MustCompile(matchStr)
	if ok, _ := regexp.MatchString(matchStr, queryString); !ok {
		return nil
	}
	condExprMatch := conditionExpr.FindStringSubmatch(queryString)
	parseExpr := condExprMatch[1]
	totalTags := strings.Split(parseExpr, ",")
	tags := make([]string, 0)
	for _, tag := range totalTags {
		tag = strings.TrimSpace(tag)
		if strings.Contains(tag, "time") {
			continue
		}
		if strings.Contains(tag, "\"") {
			tag = tag[1 : len(tag)-1]
		}
		tags = append(tags, tag)
	}
	if len(tags) == 0 {
		return nil
	}
	slices.Sort(tags)

	return tags
}

func FieldsAndAggregation(queryString string, measurementName string) (string, string) {
	var fields []string
	var FGstr string

	regStr := `(?i)SELECT\s*(.+)\s*FROM.+`
	regExpr := regexp.MustCompile(regStr)

	if ok, _ := regexp.MatchString(regStr, queryString); ok {
		match := regExpr.FindStringSubmatch(queryString)
		FGstr = match[1]
	} else {
		return "error", "error"
	}

	var aggr string
	singleField := strings.Split(FGstr, ",")
	if strings.IndexAny(singleField[0], "(") > 0 && strings.IndexAny(singleField[0], "*") < 0 {

		index := strings.IndexAny(singleField[0], "(")
		aggr = singleField[0][:index]
		aggr = strings.ToLower(aggr)

		var startIdx int
		var endIdx int
		for i := range singleField {
			for idx, ch := range singleField[i] {
				if ch == '(' {
					startIdx = idx + 1
				}
				if ch == ')' {
					endIdx = idx
				}
			}
			tmpStr := singleField[i][startIdx:endIdx]
			tmpArr := strings.Split(tmpStr, ",")
			for i := range tmpArr {
				tmpArr[i] = strings.TrimSpace(tmpArr[i])
			}
			fields = append(fields, tmpArr...)
		}

	} else if strings.IndexAny(singleField[0], "(") > 0 && strings.IndexAny(singleField[0], "*") >= 0 {
		index := strings.IndexAny(singleField[0], "(")
		aggr = singleField[0][:index]
		aggr = strings.ToLower(aggr)

		fieldMap := Fields[measurementName]
		for key := range fieldMap {
			fields = append(fields, key)
		}
		sort.Strings(fields)
	} else if strings.IndexAny(singleField[0], "(") <= 0 && strings.IndexAny(singleField[0], "*") >= 0 {
		aggr = "empty"
		fieldMap := Fields[measurementName]
		for key := range fieldMap {
			fields = append(fields, key)
		}
		tagMap := TagKV
		for _, tags := range tagMap.Measurement {
			for i := range tags {
				for tagkey, _ := range tags[i].Tag {
					fields = append(fields, tagkey)
				}
			}
		}
		sort.Strings(fields)
		fields = slices.Compact(fields)

	} else {
		aggr = "empty"
		for i := range singleField {
			singleField[i] = strings.TrimSpace(singleField[i])
		}
		fields = append(fields, singleField...)
	}
	fieldMap := Fields[measurementName]
	for i := range fields {
		datatype := fieldMap[fields[i]]
		if datatype == "" {
			datatype = "string"
		} else if datatype == "float" {
			datatype = "float64"
		} else if datatype == "integer" {
			datatype = "int64"
		}
		fields[i] = fmt.Sprintf("%s[%s]", fields[i], datatype)
	}

	var fieldsStr string
	fieldsStr = strings.Join(fields, ",")

	return fieldsStr, aggr
}

func preOrderTraverseBinaryExpr(node *influxql.BinaryExpr, tags *[]string, predicates *[]string, datatypes *[]string) (*[]string, *[]string, *[]string) {
	if node.Op != influxql.AND && node.Op != influxql.OR {
		str := node.String()
		if strings.Contains(str, "'") {
			*datatypes = append(*datatypes, "string")
		} else if strings.EqualFold(node.RHS.String(), "true") || strings.EqualFold(node.RHS.String(), "false") {
			*datatypes = append(*datatypes, "bool")
		} else if strings.Contains(str, ".") {
			*datatypes = append(*datatypes, "float64")
		} else {
			*datatypes = append(*datatypes, "int64")
		}

		tmpStr := strings.ReplaceAll(node.LHS.String(), "\"", "")
		*tags = append(*tags, tmpStr)
		str = strings.ReplaceAll(str, " ", "")
		str = strings.ReplaceAll(str, "\"", "")
		*predicates = append(*predicates, str)
		return tags, predicates, datatypes
	}

	if node.LHS != nil {
		binaryExprL := getBinaryExpr(node.LHS.String())
		preOrderTraverseBinaryExpr(binaryExprL, tags, predicates, datatypes)
	} else {
		return tags, predicates, datatypes
	}

	if node.RHS != nil {
		binaryExprR := getBinaryExpr(node.RHS.String())
		preOrderTraverseBinaryExpr(binaryExprR, tags, predicates, datatypes)
	} else {
		return tags, predicates, datatypes
	}

	return tags, predicates, datatypes
}

func getBinaryExpr(str string) *influxql.BinaryExpr {
	now := time.Now()
	valuer := influxql.NowValuer{Now: now}
	parsedExpr, _ := influxql.ParseExpr(str)
	condExpr, _, _ := influxql.ConditionExpr(parsedExpr, &valuer)
	binaryExpr := condExpr.(*influxql.BinaryExpr)

	return binaryExpr
}

func PredicatesAndTagConditions(query string, metric string, tagMap MeasurementTagMap) (string, []string) {
	regStr := `(?i).+WHERE(.+)`
	conditionExpr := regexp.MustCompile(regStr)
	if ok, _ := regexp.MatchString(regStr, query); !ok {
		return "{empty}", nil
	}
	condExprMatch := conditionExpr.FindStringSubmatch(query)
	parseExpr := condExprMatch[1]

	now := time.Now()
	valuer := influxql.NowValuer{Now: now}
	expr, _ := influxql.ParseExpr(parseExpr)
	cond, _, _ := influxql.ConditionExpr(expr, &valuer)

	tagConds := make([]string, 0)
	var result string
	if cond == nil {
		result += fmt.Sprintf("{empty}")
	} else {
		var conds []string
		var tag []string
		binaryExpr := cond.(*influxql.BinaryExpr)
		var datatype []string

		tags, predicates, datatypes := preOrderTraverseBinaryExpr(binaryExpr, &tag, &conds, &datatype)
		result += "{"
		for i, p := range *predicates {
			isTag := false
			found := false
			for _, t := range tagMap.Measurement[metric] {
				for tagkey, _ := range t.Tag {
					if (*tags)[i] == tagkey {
						isTag = true
						found = true
						break
					}
				}
				if found {
					break
				}
			}

			if !isTag {
				result += fmt.Sprintf("(%s[%s])", p, (*datatypes)[i])
			} else {
				p = strings.ReplaceAll(p, "'", "")
				tagConds = append(tagConds, p)
			}
		}
		result += "}"
	}

	if len(result) == 2 {
		result = "{empty}"
	}

	sort.Strings(tagConds)
	return result, tagConds
}

func GetMetricName(queryString string) string {
	regStr := `(?i)FROM(.+)WHERE`
	conditionExpr := regexp.MustCompile(regStr)
	if ok, _ := regexp.MatchString(regStr, queryString); !ok {
		return ""
	}
	condExprMatch := conditionExpr.FindStringSubmatch(queryString)
	parseExpr := condExprMatch[1]

	trimStr := strings.TrimSpace(parseExpr)
	trimStr = strings.ReplaceAll(trimStr, "\"", "")
	splitIndex := strings.LastIndex(trimStr, ".") + 1
	metric := trimStr[splitIndex:]

	return metric
}

var combinations []string

func combinationTagValues(allTagStr [][]string) []string {
	if len(allTagStr) == 0 {
		return []string{}
	}
	combinations = []string{}
	backtrace(allTagStr, 0, "")
	slices.Sort(combinations)
	return combinations
}

func backtrace(allTagStr [][]string, index int, combination string) {
	if index == len(allTagStr) {
		combinations = append(combinations, combination)
	} else {
		tagStr := allTagStr[index]
		valCounts := len(tagStr)
		for i := 0; i < valCounts; i++ {
			backtrace(allTagStr, index+1, combination+","+string(tagStr[i]))
		}
	}
}

func IntegratedSM(measurementName string, tagConds []string, tags []string) string {
	result := ""

	tagValues := make(map[string][]string)
	tagPre := make([]string, 0)
	for i := range tagConds {
		var idx int
		if idx = strings.Index(tagConds[i], "!"); idx < 0 {
			idx = strings.Index(tagConds[i], "=")
		}
		tagName := tagConds[i][:idx]
		tagPre = append(tagPre, tagName)
	}
	values := make([]string, 0)
	for _, tag := range tags {
		if !slices.Contains(tagPre, tag) {
			for _, tagMap := range TagKV.Measurement[measurementName] {
				if len(tagMap.Tag[tag].Values) != 0 {
					values = tagMap.Tag[tag].Values
					for _, val := range values {
						tmpTagValues := fmt.Sprintf("%s=%s", tag, val)
						tagValues[tag] = append(tagValues[tag], tmpTagValues)
					}
					break
				}
			}
		}
	}

	keys := make([]string, 0)
	for key := range tagValues {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	table_num := 1
	allTagStr := make([][]string, 0)
	for _, key := range keys {
		val := tagValues[key]
		table_num *= len(val)
		tagStr := make([]string, 0)
		for _, v := range val {
			tmp := fmt.Sprintf("%s.%s", measurementName, v)
			tagStr = append(tagStr, tmp)
		}
		allTagStr = append(allTagStr, tagStr)
	}
	groupByTags := combinationTagValues(allTagStr)
	result += "{"
	if len(tagConds) > 0 && len(groupByTags) > 0 {
		for i := range tagConds {
			for j := range groupByTags {
				tmp := ""
				if strings.Compare(tagConds[i], groupByTags[j]) >= 0 {
					tmp = fmt.Sprintf("%s.%s%s", measurementName, tagConds[i], groupByTags[j])
				} else {
					groupByTags[j] = groupByTags[j][1:len(groupByTags[j])]
					groupByTags[j] += ","
					tmp = fmt.Sprintf("%s%s.%s", groupByTags[j], measurementName, tagConds[i])
				}
				result += fmt.Sprintf("(%s)", tmp)
			}
		}
	} else if len(tagConds) == 0 && len(groupByTags) > 0 {
		for j := range groupByTags {
			tmp := fmt.Sprintf("%s", groupByTags[j])
			tmp = tmp[1:len(tmp)]
			result += fmt.Sprintf("(%s)", tmp)
		}
	} else if len(tagConds) > 0 && len(groupByTags) == 0 {
		for i := range tagConds {
			tmp := fmt.Sprintf("(%s.%s)", measurementName, tagConds[i])
			result += fmt.Sprintf("%s", tmp)
		}
	} else {
		result += fmt.Sprintf("(%s.empty)", measurementName)
	}

	result += "}"
	return result
}

func SeperateSM(integratedSM string) []string {
	integratedSM = integratedSM[1 : len(integratedSM)-1]
	sepSM := strings.Split(integratedSM, ")")
	sepSM = sepSM[:len(sepSM)-1]
	for i := range sepSM {
		sepSM[i] = sepSM[i][1:]
	}
	return sepSM
}

func GetSeperateSemanticSegment(queryString string) []string {
	results := make([]string, 0)

	queryTemplate, _, _, _ := GetQueryTemplate(queryString)
	semanticSegment := ""
	if ss, ok := QueryTemplates[queryTemplate]; !ok {

		semanticSegment = GetSemanticSegment(queryString)
		QueryTemplates[queryTemplate] = semanticSegment
		idx := strings.Index(semanticSegment, "}")
		integratedSM := semanticSegment[:idx+1]
		commonFields := semanticSegment[idx+1:]

		sepSM := SeperateSM(integratedSM)

		for i := range sepSM {
			tmp := fmt.Sprintf("{(%s)}%s", sepSM[i], commonFields)
			results = append(results, tmp)
		}

		SeprateSegments[semanticSegment] = results

		return results
	} else {
		semanticSegment = ss

		if sepseg, ok := SeprateSegments[semanticSegment]; !ok {
			idx := strings.Index(semanticSegment, "}")
			integratedSM := semanticSegment[:idx+1]
			commonFields := semanticSegment[idx+1:]

			sepSM := SeperateSM(integratedSM)

			for i := range sepSM {
				tmp := fmt.Sprintf("{(%s)}%s", sepSM[i], commonFields)
				results = append(results, tmp)
			}

			SeprateSegments[semanticSegment] = results

			return results
		} else {
			return sepseg
		}

	}

}

func GetSeparateSemanticSegmentWithNullTag(seperateSemanticSegment string, nullTags []string) string {
	if len(nullTags) == 0 {
		return ""
	}
	nullSegment := seperateSemanticSegment

	splitSlices := strings.Split(seperateSemanticSegment, nullTags[0])
	startIndex := strings.Index(splitSlices[1], "=") + 1
	branketIndex := strings.Index(splitSlices[1], "}")
	endIndex := strings.Index(splitSlices[1], ",")
	if endIndex < 0 || endIndex > branketIndex {
		endIndex = strings.Index(splitSlices[1], ")")
	}

	splitSlices[1] = strings.Replace(splitSlices[1], splitSlices[1][startIndex:endIndex], "null", 1)
	nullSegment = splitSlices[0] + nullTags[0] + splitSlices[1]

	return nullSegment
}

func GetSemanticSegment(queryString string) string {
	result := ""

	measurement := GetMetricName(queryString)
	SP, tagConds := PredicatesAndTagConditions(queryString, measurement, TagKV)
	fields, aggr := FieldsAndAggregation(queryString, measurement)
	tags := GroupByTags(queryString, measurement)
	interval := GetInterval(queryString)
	SM := IntegratedSM(measurement, tagConds, tags)

	result = fmt.Sprintf("%s#{%s}#%s#{%s,%s}", SM, fields, SP, aggr, interval)

	return result
}

func GetSemanticSegmentAndFields(queryString string) (string, string) {
	result := ""

	measurement := GetMetricName(queryString)
	SP, tagConds := PredicatesAndTagConditions(queryString, measurement, TagKV)
	fields, aggr := FieldsAndAggregation(queryString, measurement)
	tags := GroupByTags(queryString, measurement)
	interval := GetInterval(queryString)
	SM := IntegratedSM(measurement, tagConds, tags)

	result = fmt.Sprintf("%s#{%s}#%s#{%s,%s}", SM, fields, SP, aggr, interval)

	return result, fields
}

func GetPartialSegmentAndFields(queryString string) (string, string, string) {
	partialSegment := ""

	metric := GetMetricName(queryString)
	SP, _ := PredicatesAndTagConditions(queryString, metric, TagKV)
	fields, aggr := FieldsAndAggregation(queryString, metric)
	interval := GetInterval(queryString)

	partialSegment = fmt.Sprintf("#{%s}#%s#{%s,%s}", fields, SP, aggr, interval)

	return partialSegment, fields, metric
}

func GetSingleSegment(metric, partialSegment string, tags []string) []string {
	result := make([]string, 0)

	if len(tags) == 0 {
		tmpRes := fmt.Sprintf("{(%s.*)}%s", metric, partialSegment)
		result = append(result, tmpRes)
	} else {
		for _, tag := range tags {
			tmpRes := fmt.Sprintf("{(%s.%s)}%s", metric, tag, partialSegment)
			result = append(result, tmpRes)
		}
	}

	return result
}

func GetStarSegment(metric, partialSegment string) string {

	result := fmt.Sprintf("{(%s.*)}%s", metric, partialSegment)

	return result
}

func GetTotalSegment(metric string, tags []string, partialSegment string) string {
	result := ""

	if len(tags) == 0 {
		result += fmt.Sprintf("(%s.*)", metric)
	} else {
		for _, tag := range tags {
			result += fmt.Sprintf("(%s.%s)", metric, tag)
		}
	}

	result = "{" + result + "}" + partialSegment

	return result
}

func SplitPartialSegment(semanticSegment string) (string, string, string) {
	firstIdx := strings.Index(semanticSegment, "#")
	tagString := semanticSegment[:firstIdx]
	partialSegment := semanticSegment[firstIdx:]

	secondIdx := strings.Index(partialSegment[1:], "#")
	fields := partialSegment[2:secondIdx]

	mStartIdx := strings.Index(tagString, "(")
	mEndIdx := strings.Index(tagString, ".")
	metric := tagString[mStartIdx+1 : mEndIdx]

	return partialSegment, fields, metric
}
