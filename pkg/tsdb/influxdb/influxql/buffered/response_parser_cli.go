package buffered

import (
	"encoding/json"
	"errors"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data"
	influxdb_client "github.com/grafana/grafana/InfluxDB-client/v2"
	"github.com/grafana/grafana/pkg/tsdb/influxdb/influxql/util"
	"github.com/grafana/grafana/pkg/tsdb/influxdb/models"
	"strings"
	"time"
)

func ResponseParseCLI(resp *influxdb_client.Response, query *models.Query) *backend.DataResponse {
	return parseCLI(resp, query)
}

func parseCLI(response *influxdb_client.Response, query *models.Query) *backend.DataResponse {
	if response == nil || response.Error() != nil || len(response.Results) == 0 {
		return &backend.DataResponse{Error: errors.New("result.Err")}
	}
	result := response.Results[0]
	if result.Err != "" {
		return &backend.DataResponse{Error: errors.New(result.Err)}
	}

	rows := make([]models.Row, 0)

	for _, row := range result.Series {
		rows = append(rows, models.Row{
			Name:    row.Name,
			Tags:    row.Tags,
			Columns: row.Columns,
			Values:  row.Values,
		})
	}

	if query.ResultFormat == "table" {
		return &backend.DataResponse{Frames: transformRowsForTable(rows, *query)}
	}

	return &backend.DataResponse{Frames: transformRowsForTimeSeriesCLI(rows, *query)}
}

func transformRowsForTimeSeriesCLI(rows []models.Row, query models.Query) data.Frames {
	// pre-allocate frames - this can save many allocations
	cols := 0
	for _, row := range rows {
		cols += len(row.Columns)
	}

	if len(rows) == 0 {
		return make([]*data.Frame, 0)
	}

	// Preallocate for the worst-case scenario
	frames := make([]*data.Frame, 0, len(rows)*len(rows[0].Columns))

	// frameName is pre-allocated. So we can reuse it, saving memory.
	// It's sized for a reasonably-large name, but will grow if needed.
	frameName := make([]byte, 0, 128)

	for _, row := range rows {
		var hasTimeCol = false

		for _, column := range row.Columns {
			if strings.ToLower(column) == "time" {
				hasTimeCol = true
			}
		}

		if !hasTimeCol {
			newFrame := newFrameWithoutTimeField(row, query)
			frames = append(frames, newFrame)
		} else {
			for colIndex, column := range row.Columns {
				if column == "time" {
					continue
				}
				newFrame := newFrameWithTimeFieldCLI(row, column, colIndex, query, frameName)
				frames = append(frames, newFrame)
			}
		}
	}

	if len(frames) > 0 {
		frames[0].Meta = &data.FrameMeta{
			ExecutedQueryString:    query.RawQuery,
			PreferredVisualization: util.GetVisType(query.ResultFormat),
		}
	}

	return frames
}

func newFrameWithTimeFieldCLI(row models.Row, column string, colIndex int, query models.Query, frameName []byte) *data.Frame {
	var floatArray []*float64
	var stringArray []*string
	var boolArray []*bool
	valType := util.Typeof(row.Values, colIndex)

	timeArray := make([]time.Time, 0, len(row.Values))
	for _, valuePair := range row.Values {
		timestamp, timestampErr := util.ParseTimestamp(valuePair[0])
		// we only add this row if the timestamp is valid
		//if timestampErr != nil {
		//	continue
		//}

		if timestampErr != nil {
			its, err := valuePair[0].(json.Number).Int64()
			if err != nil {
				uts := int64(valuePair[0].(uint64))
				timeArray = append(timeArray, time.Unix(uts, 0))
			} else {
				timeArray = append(timeArray, time.Unix(its, 0))
			}
		} else {
			timeArray = append(timeArray, timestamp)
		}

		switch valType {
		case "string":
			value, ok := valuePair[colIndex].(string)
			if ok {
				stringArray = append(stringArray, &value)
			} else {
				stringArray = append(stringArray, nil)
			}
		case "json.Number":
			value := util.ParseNumber(valuePair[colIndex])
			floatArray = append(floatArray, value)
		case "float64":
			if value, ok := valuePair[colIndex].(float64); ok {
				floatArray = append(floatArray, &value)
			} else {
				floatArray = append(floatArray, nil)
			}
		case "bool":
			value, ok := valuePair[colIndex].(bool)
			if ok {
				boolArray = append(boolArray, &value)
			} else {
				boolArray = append(boolArray, nil)
			}
		case "null":
			floatArray = append(floatArray, nil)
		}
	}

	timeField := data.NewField("Time", nil, timeArray)

	var valueField *data.Field

	switch valType {
	case "string":
		valueField = data.NewField("Value", row.Tags, stringArray)
	case "json.Number":
		valueField = data.NewField("Value", row.Tags, floatArray)
	case "float64":
		valueField = data.NewField("Value", row.Tags, floatArray)
	case "bool":
		valueField = data.NewField("Value", row.Tags, boolArray)
	case "null":
		valueField = data.NewField("Value", row.Tags, floatArray)
	}

	name := string(util.FormatFrameName(row.Name, column, row.Tags, query, frameName[:]))
	valueField.SetConfig(&data.FieldConfig{DisplayNameFromDS: name})
	return data.NewFrame(name, timeField, valueField)
}
