package influxql

import (
	"context"
	"fmt"
	influxdb_client "github.com/grafana/grafana/InfluxDB-client/v2"
	"sync"

	"github.com/grafana/dskit/concurrency"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"go.opentelemetry.io/otel/trace"

	"github.com/grafana/grafana/pkg/services/featuremgmt"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/tsdb/influxdb/influxql/buffered"
	"github.com/grafana/grafana/pkg/tsdb/influxdb/models"
)

func QueryCLI(ctx context.Context, tracer trace.Tracer, dsInfo *models.DatasourceInfo, req *backend.QueryDataRequest, features featuremgmt.FeatureToggles) (*backend.QueryDataResponse, error) {
	logger := glog.FromContext(ctx)
	response := backend.NewQueryDataResponse()
	var err error

	// We are testing running of queries in parallel behind feature flag
	if features.IsEnabled(ctx, featuremgmt.FlagInfluxdbRunQueriesInParallel) {
		concurrentQueryCount, err := req.PluginContext.GrafanaConfig.ConcurrentQueryCount()
		if err != nil {
			logger.Debug(fmt.Sprintf("Concurrent Query Count read/parse error: %v", err), featuremgmt.FlagInfluxdbRunQueriesInParallel)
			concurrentQueryCount = 10
		}

		responseLock := sync.Mutex{}
		err = concurrency.ForEachJob(ctx, len(req.Queries), concurrentQueryCount, func(ctx context.Context, idx int) error {
			reqQuery := req.Queries[idx]
			query, err := models.QueryParse(reqQuery)
			if err != nil {
				return err
			}

			rawQuery, err := query.Build(req)
			if err != nil {
				return err
			}

			query.RefID = reqQuery.RefID
			query.RawQuery = rawQuery

			if setting.Env == setting.Dev {
				logger.Debug("Influxdb query", "raw query", rawQuery)
			}

			resp, err := executeCLI(ctx, tracer, dsInfo, query)

			responseLock.Lock()
			defer responseLock.Unlock()
			if err != nil {
				response.Responses[query.RefID] = backend.DataResponse{Error: err}
			} else {
				response.Responses[query.RefID] = resp
			}
			return nil // errors are saved per-query,always return nil
		})

		if err != nil {
			logger.Debug("Influxdb concurrent query error", "concurrent query", err)
		}
	} else {
		for _, reqQuery := range req.Queries {
			query, err := models.QueryParse(reqQuery)
			if err != nil {
				return &backend.QueryDataResponse{}, err
			}

			rawQuery, err := query.Build(req)
			if err != nil {
				return &backend.QueryDataResponse{}, err
			}

			query.RefID = reqQuery.RefID
			query.RawQuery = rawQuery

			if setting.Env == setting.Dev {
				logger.Debug("Influxdb query", "raw query", rawQuery)
			}

			fmt.Println("Influxdb query: ", rawQuery)
			resp, err := executeCLI(ctx, tracer, dsInfo, query)

			if err != nil {
				response.Responses[query.RefID] = backend.DataResponse{Error: err}
			} else {
				response.Responses[query.RefID] = resp
			}
		}
	}

	return response, err
}

func executeCLI(ctx context.Context, tracer trace.Tracer, dsInfo *models.DatasourceInfo, query *models.Query) (backend.DataResponse, error) {

	// ************************************* //
	res, _, _ := influxdb_client.STsCacheClientSegGrafana(dsInfo.DbName, query.RawQuery, "{(cpu.hostname=host_1)}#{usage_system[int64],usage_idle[int64],usage_nice[int64]}#{empty}#{mean,5m}")

	_, endSpan := startTrace(ctx, tracer, "datasource.influxdb.influxql.parseResponse")
	defer endSpan()

	var resp *backend.DataResponse

	resp = buffered.ResponseParseCLI(res, query)

	//if len(resp.Frames) > 0 {
	//	resp.Frames[0].Meta.Custom = readCustomMetadata(res)
	//}

	return *resp, nil
}
