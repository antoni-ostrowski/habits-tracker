package obs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ErrNoEndpoint is returned when OTEL_EXPORTER_OTLP_ENDPOINT is not set.
// Callers should log a warning and keep running: global providers stay noop
// (telemetry is dropped) while the stderr slog handler keeps working.
var ErrNoEndpoint = errors.New("obs: OTEL_EXPORTER_OTLP_ENDPOINT not set, telemetry disabled")

// SetupOTelSDK bootstraps the OpenTelemetry pipeline for serviceName, exporting
// OTLP/HTTP. Each signal resolves its endpoint from its own variable first
// (OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, OTEL_EXPORTER_OTLP_METRICS_ENDPOINT,
// OTEL_EXPORTER_OTLP_LOGS_ENDPOINT), falling back to OTEL_EXPORTER_OTLP_ENDPOINT.
// A signal with no endpoint stays on its global noop provider. When none are
// set it returns (nil, ErrNoEndpoint) and sets nothing: the app keeps running.
// Tests should skip this entirely and use noop tracers/meters.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupOTelSDK(ctx context.Context, serviceName string) (func(context.Context) error, error) {
	traceEndpoint, traceInsecure, traceOK := endpointFor("TRACES")
	meterEndpoint, meterInsecure, meterOK := endpointFor("METRICS")
	logEndpoint, logInsecure, logOK := endpointFor("LOGS")
	wanted := traceOK || meterOK || logOK
	if traceOK && !probeTarget(traceEndpoint) {
		traceOK = false
	}
	if meterOK && !probeTarget(meterEndpoint) {
		meterOK = false
	}
	if logOK && !probeTarget(logEndpoint) {
		logOK = false
	}
	if !traceOK && !meterOK && !logOK {
		if wanted {
			return nil, fmt.Errorf("obs: OTLP collector unreachable, telemetry disabled: %w", ErrNoEndpoint)
		}
		return nil, ErrNoEndpoint
	}

	var shutdownFuncs []func(context.Context) error
	var err error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	res, err := newResource(ctx, serviceName)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}

	// Set up trace provider.
	if traceOK {
		tracerProvider, err := newTracerProvider(ctx, res, traceEndpoint, traceInsecure)
		if err != nil {
			handleErr(err)
			return shutdown, err
		}
		shutdownFuncs = append(shutdownFuncs, tracerProvider.Shutdown)
		otel.SetTracerProvider(tracerProvider)
	}

	// Set up meter provider.
	if meterOK {
		meterProvider, err := newMeterProvider(ctx, res, meterEndpoint, meterInsecure)
		if err != nil {
			handleErr(err)
			return shutdown, err
		}
		shutdownFuncs = append(shutdownFuncs, meterProvider.Shutdown)
		otel.SetMeterProvider(meterProvider)
	}

	// Set up logger provider.
	if logOK {
		loggerProvider, err := newLoggerProvider(ctx, res, logEndpoint, logInsecure)
		if err != nil {
			handleErr(err)
			return shutdown, err
		}
		shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
		global.SetLoggerProvider(loggerProvider)
	}

	return shutdown, err
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(semconv.ServiceNameKey.String(serviceName)),
	)
}

// endpointFor resolves the OTLP target for one signal (TRACES, METRICS, LOGS).
// The signal-specific OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT wins; the general
// OTEL_EXPORTER_OTLP_ENDPOINT is the fallback. ok is false when neither is set.
func endpointFor(signal string) (endpoint string, insecure, ok bool) {
	if raw := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_" + signal + "_ENDPOINT")); raw != "" {
		endpoint, insecure := splitTarget(raw)
		return endpoint, insecure, true
	}
	endpoint, insecure = otlpTarget()
	if endpoint == "" {
		return "", false, false
	}
	return endpoint, insecure, true
}

// otlpTarget reads OTEL_EXPORTER_OTLP_ENDPOINT ("https://collector:4318" or
// bare "collector:4318"). Empty means telemetry stays disabled (noop).
func otlpTarget() (endpoint string, insecure bool) {
	return splitTarget(strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")))
}

// splitTarget strips an optional scheme ("http" implies insecure) and returns
// the host:port the OTLP HTTP exporters expect.
func splitTarget(raw string) (endpoint string, insecure bool) {
	if raw == "" {
		return "", false
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host, u.Scheme == "http"
	}
	return raw, true // bare host:port, assume local http
}

// probeTarget reports whether endpoint (host:port) accepts TCP. A refused
// or timed-out dial means no collector is listening, so the signal stays
// noop instead of creating an exporter that retries and spams logs.
func probeTarget(endpoint string) bool {
	conn, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func newTracerProvider(ctx context.Context, res *resource.Resource, endpoint string, insecure bool) (*trace.TracerProvider, error) {
	opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	traceExporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return trace.NewTracerProvider(
		trace.WithBatcher(traceExporter),
		trace.WithResource(res),
	), nil
}

func newMeterProvider(ctx context.Context, res *resource.Resource, endpoint string, insecure bool) (*metric.MeterProvider, error) {
	opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlpmetrichttp.WithInsecure())
	}
	metricExporter, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter)),
	), nil
}

func newLoggerProvider(ctx context.Context, res *resource.Resource, endpoint string, insecure bool) (*log.LoggerProvider, error) {
	opts := []otlploghttp.Option{otlploghttp.WithEndpoint(endpoint)}
	if insecure {
		opts = append(opts, otlploghttp.WithInsecure())
	}
	logExporter, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return log.NewLoggerProvider(
		log.WithResource(res),
		log.WithProcessor(log.NewBatchProcessor(logExporter)),
	), nil
}
