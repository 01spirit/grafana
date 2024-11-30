package sqleng

import (
	"context"
	"fmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	"strings"
	"time"
)

type dataQueryModelCLI struct {
	InterpolatedQuery string // property not set until after Interpolate()
	Format            dataQueryFormat
	TimeRange         backend.TimeRange
	FillMissing       *data.FillMissing // property not set until after Interpolate()
	Interval          time.Duration
	columnNames       []string
	columnTypes       []string
	timeIndex         int
	timeEndIndex      int
	metricIndex       int
	metricPrefix      bool
	queryContext      context.Context
}

func (e *DataSourceHandler) newProcessCfgCLI(query backend.DataQuery, queryContext context.Context, interpolatedQuery string, columnNames []string) (*dataQueryModelCLI, error) {
	columnTypes := make([]string, len(columnNames))
	for i := range columnNames {
		if i == 0 {
			columnTypes[i] = "BIGINT"
		} else if i == 1 {
			columnTypes[i] = "TEXT"
		} else {
			columnTypes[i] = "DECIMAL"
		}
	}

	qm := &dataQueryModelCLI{
		columnTypes:  columnTypes,
		columnNames:  columnNames,
		timeIndex:    -1,
		timeEndIndex: -1,
		metricIndex:  -1,
		metricPrefix: false,
		queryContext: queryContext,
	}

	qm.TimeRange.From = query.TimeRange.From.UTC()
	qm.TimeRange.To = query.TimeRange.To.UTC()
	qm.Format = dataQueryFormatSeries

	//fmt.Println(qm.columnNames)
	//fmt.Println(qm.columnTypes)
	for i, col := range qm.columnNames {
		for _, tc := range e.timeColumnNames {
			if col == tc {
				qm.timeIndex = i
				break
			}
		}

		if qm.Format == dataQueryFormatTable && col == "timeend" {
			qm.timeEndIndex = i
			continue
		}

		switch col {
		case "metric":
			qm.metricIndex = i
		default:
			if qm.metricIndex == -1 {
				columnType := qm.columnTypes[i]
				for _, mct := range e.metricColumnTypes {
					if columnType == mct {
						qm.metricIndex = i
						continue
					}
				}
			}
		}
	}
	qm.InterpolatedQuery = interpolatedQuery
	return qm, nil
}

func FrameFromRowsInterface(rowsValues [][]interface{}, rowLimit int64, names []string) (*data.Frame, error) {
	frame := NewFrame(names)

	var i int64
	for {

		if i == rowLimit {
			frame.AppendNotices(data.Notice{
				Severity: data.NoticeSeverityWarning,
				Text:     fmt.Sprintf("Results have been limited to %v because the SQL row limit was reached", rowLimit),
			})
			break
		}

		if err := Append(frame, rowsValues[i]); err != nil {
			return nil, err
		}

		i++

		if i == rowLimit || i == int64(len(rowsValues)) {
			break
		}
	}

	return frame, nil
}

// NewFrame creates a new data.Frame with empty fields given the columns and converters
func NewFrame(columns []string) *data.Frame {
	fields := make(data.Fields, len(columns))
	fieldTypes := make([]data.FieldType, len(columns))
	for i := range columns {
		if i == 0 {
			//fieldTypes[i] = data.FieldTypeNullableInt64
			fieldTypes[i] = data.FieldTypeTime
		} else if i == 1 {
			fieldTypes[i] = data.FieldTypeString
		} else {
			fieldTypes[i] = data.FieldTypeFloat64
		}
	}

	for i := range columns {
		fields[i] = data.NewFieldFromFieldType(fieldTypes[i], 0)
		fields[i].Name = columns[i]
	}

	return data.NewFrame("", fields...)
}

// Append appends the row to the dataframe, using the converters to convert the scanned value into a value that can be put into a data.Frame
func Append(frame *data.Frame, rowValue []interface{}) error {
	d := make([]interface{}, len(rowValue))
	for i, v := range rowValue {
		d[i] = v
	}

	frame.AppendRow(d...)
	return nil
}

func convertSQLTimeColumnsToEpochMSCLI(frame *data.Frame, qm *dataQueryModelCLI) error {
	if qm.timeIndex != -1 {
		if err := convertSQLTimeColumnToEpochMS(frame, qm.timeIndex); err != nil {
			return fmt.Errorf("%v: %w", "failed to convert time column", err)
		}
	}

	if qm.timeEndIndex != -1 {
		if err := convertSQLTimeColumnToEpochMS(frame, qm.timeEndIndex); err != nil {
			return fmt.Errorf("%v: %w", "failed to convert timeend column", err)
		}
	}

	return nil
}

// SELECT time_bucket('5 minute', time) as bucket,name,avg(latitude) FROM readings WHERE name IN ('truck_0') AND time >= '2022-12-29 06:00:00 +0000' AND time < '2022-12-30 06:00:00 +0000' GROUP BY name,bucket ORDER BY name,bucket
// segment := "{(readings.name=truck_0)}#{latitude[float64]}#{empty}#{mean,5m}"
func SQLsegment(queryString string) string {
	segment := ""
	queryString = strings.ReplaceAll(queryString, " ", "")
	tm := queryString[strings.Index(queryString, "time_bucket('")+13 : strings.Index(queryString, "',time)")]
	//fmt.Println(queryString)
	//fmt.Println(tm)
	idx := -1
	for i, ch := range tm {
		if ch > '9' || ch < '0' {
			idx = i
			break
		}
	}
	tmt := tm[:idx]
	tma := tm[idx:]

	metric := queryString[strings.Index(strings.ToUpper(queryString), "FROM")+4 : strings.Index(strings.ToUpper(queryString), "WHERE")]
	devices := queryString[strings.Index(queryString, "IN(")+3 : strings.Index(queryString, ")AND")]
	sm := ""
	deviceNames := strings.Split(devices, ",")
	for _, dev := range deviceNames {
		sm += fmt.Sprintf("(%s.name=%s)", metric, dev)
	}

	fieldClause := queryString[strings.Index(queryString, "name,")+5 : strings.Index(strings.ToUpper(queryString), "FROM")]
	fields := strings.Split(fieldClause, ",")
	for i := range fields {
		if strings.Index(fields[i], "(") > 0 {
			fields[i] = fields[i][strings.Index(fields[i], "(")+1 : strings.Index(fields[i], ")")]
		}
		fields[i] += "[float64]"
	}
	sf := strings.Join(fields, ",")

	aggr := fieldClause[:strings.Index(fieldClause, "(")]
	if strings.Compare(strings.ToLower(aggr), "avg") == 0 {
		aggr = "mean"
	}
	aggregation := fmt.Sprintf("%s,%s%s", aggr, tmt, strings.ToLower(tma[:1]))

	segment = fmt.Sprintf("{%s}#{%s}#{empty}#{%s}", sm, sf, aggregation)

	return segment
}
