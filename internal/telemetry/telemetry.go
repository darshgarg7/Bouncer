package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	OTLPEndpoint   string
	SampleRatio    float64
}

func Setup(ctx context.Context, config Config) (func(context.Context) error, error) {
	if strings.TrimSpace(config.ServiceName) == "" {
		return nil, errors.New("telemetry service name is required")
	}
	if config.SampleRatio == 0 {
		config.SampleRatio = 1
	}
	if config.SampleRatio < 0 || config.SampleRatio > 1 {
		return nil, errors.New("telemetry sample ratio must be between 0 and 1")
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if strings.TrimSpace(config.OTLPEndpoint) == "" {
		return func(context.Context) error { return nil }, nil
	}
	parsed, err := url.Parse(config.OTLPEndpoint)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("OTLP endpoint must be an absolute HTTP(S) URL")
	}
	exporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpointURL(config.OTLPEndpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))),
		sdktrace.WithResource(resource.NewWithAttributes(
			"https://opentelemetry.io/schemas/1.37.0",
			attribute.String("service.name", config.ServiceName),
			attribute.String("service.version", config.ServiceVersion),
		)),
	)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
