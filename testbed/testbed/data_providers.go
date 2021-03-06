// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package testbed

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math/rand"
	"strconv"
	"time"

	"go.uber.org/atomic"

	"go.opentelemetry.io/collector/consumer/pdata"
	"go.opentelemetry.io/collector/consumer/pdatautil"
	"go.opentelemetry.io/collector/internal/data"
	otlptrace "go.opentelemetry.io/collector/internal/data/opentelemetry-proto-gen/trace/v1"
	"go.opentelemetry.io/collector/internal/goldendataset"
)

// DataProvider defines the interface for generators of test data used to drive various end-to-end tests.
type DataProvider interface {
	// SetLoadGeneratorCounters supplies pointers to LoadGenerator counters.
	// The data provider implementation should increment these as it generates data.
	SetLoadGeneratorCounters(batchesGenerated *atomic.Uint64, dataItemsGenerated *atomic.Uint64)
	// GenerateTraces returns an internal Traces instance with an OTLP ResourceSpans slice populated with test data.
	GenerateTraces() (pdata.Traces, bool)
	// GenerateMetrics returns an internal MetricData instance with an OTLP ResourceMetrics slice of test data.
	GenerateMetrics() (pdata.Metrics, bool)
	// GetGeneratedSpan returns the generated Span matching the provided traceId and spanId or else nil if no match found.
	GetGeneratedSpan(traceID []byte, spanID []byte) *otlptrace.Span
}

// PerfTestDataProvider in an implementation of the DataProvider for use in performance tests.
// Tracing IDs are based on the incremented batch and data items counters.
type PerfTestDataProvider struct {
	options            LoadOptions
	batchesGenerated   *atomic.Uint64
	dataItemsGenerated *atomic.Uint64
}

// NewPerfTestDataProvider creates an instance of PerfTestDataProvider which generates test data based on the sizes
// specified in the supplied LoadOptions.
func NewPerfTestDataProvider(options LoadOptions) *PerfTestDataProvider {
	return &PerfTestDataProvider{
		options: options,
	}
}

func (dp *PerfTestDataProvider) SetLoadGeneratorCounters(batchesGenerated *atomic.Uint64, dataItemsGenerated *atomic.Uint64) {
	dp.batchesGenerated = batchesGenerated
	dp.dataItemsGenerated = dataItemsGenerated
}

func (dp *PerfTestDataProvider) GenerateTraces() (pdata.Traces, bool) {

	traceData := pdata.NewTraces()
	traceData.ResourceSpans().Resize(1)
	ilss := traceData.ResourceSpans().At(0).InstrumentationLibrarySpans()
	ilss.Resize(1)
	spans := ilss.At(0).Spans()
	spans.Resize(dp.options.ItemsPerBatch)

	traceID := dp.batchesGenerated.Inc()
	for i := 0; i < dp.options.ItemsPerBatch; i++ {

		startTime := time.Now()
		endTime := startTime.Add(time.Millisecond)

		spanID := dp.dataItemsGenerated.Inc()

		span := spans.At(i)

		// Create a span.
		span.SetTraceID(GenerateSequentialTraceID(traceID))
		span.SetSpanID(GenerateSequentialSpanID(spanID))
		span.SetName("load-generator-span")
		span.SetKind(pdata.SpanKindCLIENT)
		attrs := span.Attributes()
		attrs.UpsertInt("load_generator.span_seq_num", int64(spanID))
		attrs.UpsertInt("load_generator.trace_seq_num", int64(traceID))
		// Additional attributes.
		for k, v := range dp.options.Attributes {
			attrs.UpsertString(k, v)
		}
		span.SetStartTime(pdata.TimestampUnixNano(uint64(startTime.UnixNano())))
		span.SetEndTime(pdata.TimestampUnixNano(uint64(endTime.UnixNano())))
	}
	return traceData, false
}

func GenerateSequentialTraceID(id uint64) []byte {
	var traceID [16]byte
	binary.PutUvarint(traceID[:], id)
	return traceID[:]
}

func GenerateSequentialSpanID(id uint64) []byte {
	var spanID [8]byte
	binary.PutUvarint(spanID[:], id)
	return spanID[:]
}

func (dp *PerfTestDataProvider) GenerateMetrics() (pdata.Metrics, bool) {

	// Generate 7 data points per metric.
	const dataPointsPerMetric = 7

	metricData := data.NewMetricData()
	metricData.ResourceMetrics().Resize(1)
	metricData.ResourceMetrics().At(0).InstrumentationLibraryMetrics().Resize(1)
	if dp.options.Attributes != nil {
		attrs := metricData.ResourceMetrics().At(0).Resource().Attributes()
		attrs.InitEmptyWithCapacity(len(dp.options.Attributes))
		for k, v := range dp.options.Attributes {
			attrs.UpsertString(k, v)
		}
	}
	metrics := metricData.ResourceMetrics().At(0).InstrumentationLibraryMetrics().At(0).Metrics()
	metrics.Resize(dp.options.ItemsPerBatch)

	for i := 0; i < dp.options.ItemsPerBatch; i++ {
		metric := metrics.At(i)
		metricDescriptor := metric.MetricDescriptor()
		metricDescriptor.InitEmpty()
		metricDescriptor.SetName("load_generator_" + strconv.Itoa(i))
		metricDescriptor.SetDescription("Load Generator Counter #" + strconv.Itoa(i))
		metricDescriptor.SetType(pdata.MetricTypeInt64)

		batchIndex := dp.batchesGenerated.Inc()

		// Generate data points for the metric.
		metric.Int64DataPoints().Resize(dataPointsPerMetric)
		for j := 0; j < dataPointsPerMetric; j++ {
			dataPoint := metric.Int64DataPoints().At(j)
			dataPoint.SetStartTime(pdata.TimestampUnixNano(uint64(time.Now().UnixNano())))
			value := dp.dataItemsGenerated.Inc()
			dataPoint.SetValue(int64(value))
			dataPoint.LabelsMap().InitFromMap(map[string]string{
				"item_index":  "item_" + strconv.Itoa(j),
				"batch_index": "batch_" + strconv.Itoa(int(batchIndex)),
			})
		}
	}
	return pdatautil.MetricsFromInternalMetrics(metricData), false
}

func (dp *PerfTestDataProvider) GetGeneratedSpan([]byte, []byte) *otlptrace.Span {
	// function not supported for this data provider
	return nil
}

// GoldenDataProvider is an implementation of DataProvider for use in correctness tests.
// Provided data from the "Golden" dataset generated using pairwise combinatorial testing techniques.
type GoldenDataProvider struct {
	tracePairsFile     string
	spanPairsFile      string
	random             io.Reader
	batchesGenerated   *atomic.Uint64
	dataItemsGenerated *atomic.Uint64
	resourceSpans      []*otlptrace.ResourceSpans
	spansIndex         int
	spansMap           map[string]*otlptrace.Span
}

// NewGoldenDataProvider creates a new instance of GoldenDataProvider which generates test data based
// on the pairwise combinations specified in the tracePairsFile and spanPairsFile input variables.
// The supplied randomSeed is used to initialize the random number generator used in generating tracing IDs.
func NewGoldenDataProvider(tracePairsFile string, spanPairsFile string, randomSeed int64) *GoldenDataProvider {
	return &GoldenDataProvider{
		tracePairsFile: tracePairsFile,
		spanPairsFile:  spanPairsFile,
		random:         io.Reader(rand.New(rand.NewSource(randomSeed))),
	}
}

func (dp *GoldenDataProvider) SetLoadGeneratorCounters(batchesGenerated *atomic.Uint64, dataItemsGenerated *atomic.Uint64) {
	dp.batchesGenerated = batchesGenerated
	dp.dataItemsGenerated = dataItemsGenerated
}

func (dp *GoldenDataProvider) GenerateTraces() (pdata.Traces, bool) {
	if dp.resourceSpans == nil {
		var err error
		dp.resourceSpans, err = goldendataset.GenerateResourceSpans(dp.tracePairsFile, dp.spanPairsFile, dp.random)
		if err != nil {
			log.Printf("cannot generate traces: %s", err)
			dp.resourceSpans = make([]*otlptrace.ResourceSpans, 0)
		}
	}
	dp.batchesGenerated.Inc()
	if dp.spansIndex >= len(dp.resourceSpans) {
		return pdata.TracesFromOtlp(make([]*otlptrace.ResourceSpans, 0)), true
	}
	resourceSpans := make([]*otlptrace.ResourceSpans, 1)
	resourceSpans[0] = dp.resourceSpans[dp.spansIndex]
	dp.spansIndex++
	spanCount := uint64(0)
	for _, libSpans := range resourceSpans[0].InstrumentationLibrarySpans {
		spanCount += uint64(len(libSpans.Spans))
	}
	dp.dataItemsGenerated.Add(spanCount)
	return pdata.TracesFromOtlp(resourceSpans), false
}

func (dp *GoldenDataProvider) GenerateMetrics() (pdata.Metrics, bool) {
	return pdatautil.MetricsFromInternalMetrics(data.MetricData{}), true
}

func (dp *GoldenDataProvider) GetGeneratedSpan(traceID []byte, spanID []byte) *otlptrace.Span {
	if dp.spansMap == nil {
		dp.spansMap = populateSpansMap(dp.resourceSpans)
	}
	key := traceIDAndSpanIDToString(traceID, spanID)
	return dp.spansMap[key]
}

func populateSpansMap(resourceSpansList []*otlptrace.ResourceSpans) map[string]*otlptrace.Span {
	spansMap := make(map[string]*otlptrace.Span)
	for _, resourceSpans := range resourceSpansList {
		for _, libSpans := range resourceSpans.InstrumentationLibrarySpans {
			for _, span := range libSpans.Spans {
				key := traceIDAndSpanIDToString(span.TraceId, span.SpanId)
				spansMap[key] = span
			}
		}
	}
	return spansMap
}

func traceIDAndSpanIDToString(traceID []byte, spanID []byte) string {
	return fmt.Sprintf("%s-%s", hex.EncodeToString(traceID), hex.EncodeToString(spanID))
}
