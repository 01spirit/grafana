package timescaledb_client

import (
	"database/sql"
	"fmt"
	"github.com/pkg/errors"
	"regexp"
	"sort"
	"strings"
	"time"
)

const goTimeFmt = "2006-01-02 15:04:05.999999 -0700"

func ResponseIsEmpty(rows *sql.Rows) bool {
	if rows == nil {
		return true
	}
	if rows.Err() != nil {
		return true
	}

	return false
}

func GetQueryTemplate(queryString string) (string, int64, int64, []string) {
	var startTime int64
	var endTime int64
	var tags []string

	timeReg := regexp.MustCompile(`\d{4}-\d{2}-\d{2}\s\d{2}:\d{2}:\d{2}\s[+-]\d{4}`)
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

	tagReg := `(?i)WHERE (.+) AND time >=`
	conditionExpr := regexp.MustCompile(tagReg)
	if ok, _ := regexp.MatchString(tagReg, queryString); !ok {
		return "", 0, 0, nil
	}
	tagExprMatch := conditionExpr.FindStringSubmatch(result)
	tagString := tagExprMatch[1]
	result = strings.ReplaceAll(result, tagString, replacement)

	nidx := strings.Index(tagString, " ")
	name := tagString[:nidx]
	bsidx := strings.Index(tagString, "(")
	beidx := strings.Index(tagString, ")")
	tagString = tagString[bsidx+1 : beidx]

	tagString = strings.ReplaceAll(tagString, "\"", "")
	tagString = strings.ReplaceAll(tagString, "'", "")
	tagString = strings.ReplaceAll(tagString, " ", "")

	tags = strings.Split(tagString, ",")
	sort.Strings(tags)

	for i := range tags {
		tags[i] = fmt.Sprintf("%s=%s", name, tags[i])
	}

	return result, startTime, endTime, tags
}

func EmptyResultByteArray(segment string) []byte {
	emptyValues := make([]byte, 0)
	zero, _ := Int64ToByteArray(int64(0))
	emptyValues = append(emptyValues, []byte(segment)...)
	emptyValues = append(emptyValues, []byte(" ")...)
	emptyValues = append(emptyValues, zero...)

	return emptyValues
}

func RowsToInterface(rows *sql.Rows, colLen int) [][]interface{} {
	if rows == nil {
		return nil
	}
	if rows.Err() != nil {
		return nil
	}
	results := make([][]interface{}, 0)
	for rows.Next() {
		values := make([]interface{}, colLen)
		for i := range values {
			values[i] = new(interface{})
		}
		err := rows.Scan(values...)
		if err != nil {
			panic(errors.Wrap(err, "error while reading values"))
		}

		data := make([]interface{}, 0)
		for i := range values {
			data = append(data, *values[i].(*interface{}))
		}

		results = append(results, data)
	}

	rows.Close()

	return results
}

func DataTypeFromColumn(colLen int) []string {
	if colLen <= 2 {
		return nil
	}

	results := make([]string, 0)

	results = append(results, "int64")
	results = append(results, "string")

	for i := 0; i < colLen-2; i++ {
		results = append(results, "float64")
	}

	return results
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

func TimeInt64ToString(number int64) string {
	layout := goTimeFmt
	t := time.Unix(number, 0)
	timestamp := t.Format(layout)

	return timestamp
}

func TimeStringToInt64(timestamp string) int64 {
	layout := goTimeFmt
	timeT, _ := time.Parse(layout, timestamp)
	numberN := timeT.Unix()

	return numberN
}
