package log

import (
	"context"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	tracer     trace.Tracer
	initOnce   sync.Once
	shutdownFn func()
)

func GetTracer() trace.Tracer {
	initOnce.Do(func() {
		if isOtelEnabled() {
			shutdownFn = initTracer()
			tracer = otel.Tracer("op-geth")
		} else {
			tracer = trace.NewNoopTracerProvider().Tracer("nop")
		}
	})
	return tracer
}

func isOtelEnabled() bool {
	raw := os.Getenv("ENABLE_OTEL_TRACING")
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}

func ShutdownTracer() {
	if shutdownFn != nil {
		shutdownFn()
	}
}

func initTracer() func() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	res, err := newResource(ctx)
	reportErr(err, "failed to create resource")

	otcURL := os.Getenv("OTLP_RECEIVER_URL")
	conn, err := grpc.DialContext(
		ctx,
		otcURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	reportErr(err, "failed to dial OTLP collector")

	exporter, err := newExporter(ctx, conn)
	reportErr(err, "failed to create trace exporter")

	bsp := sdktrace.NewBatchSpanProcessor(
		exporter,
		sdktrace.WithMaxQueueSize(16000),
		sdktrace.WithMaxExportBatchSize(1024),
		sdktrace.WithBatchTimeout(2*time.Second),
	)

	tp := newTraceProvider(res, bsp)
	otel.SetTracerProvider(tp)

	return func() {
		reportErr(tp.Shutdown(ctx), "failed to shutdown tracer provider")
		cancel()
	}
}

// newTraceProvider constructs a TracerProvider given resource and span processor.
func newTraceProvider(res *resource.Resource, bsp sdktrace.SpanProcessor) *sdktrace.TracerProvider {
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
}

// newExporter creates an OTLP gRPC trace exporter over the given connection.
func newExporter(ctx context.Context, conn *grpc.ClientConn) (*otlptrace.Exporter, error) {
	return otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
}

// newResource defines service resource attributes for traces.
func newResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("op-geth"),
			attribute.String("op-geth", "otel-tracing"),
		),
	)
}

// reportErr logs any initialization or shutdown errors.
func reportErr(err error, message string) {
	if err != nil {
		log.Printf("%s: %v", message, err)
	}
}
