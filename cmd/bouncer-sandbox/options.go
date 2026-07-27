package main

import (
	"flag"
	"fmt"
	"io"
	"math"
)

type sandboxOptions struct {
	Listen               string
	AllowUnauthenticated bool
	IdempotencyDirectory string
	BackendMode          string
	WorkspaceRoot        string
	OTLPEndpoint         string
	TraceSampleRatio     float64
	MaxBodyBytes         int64
	RequestsPerSecond    float64
	RequestBurst         int
}

func parseOptions(arguments []string) (sandboxOptions, error) {
	options := sandboxOptions{}
	flags := flag.NewFlagSet("bouncer-sandbox", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.Listen, "listen", "127.0.0.1:8082", "sandbox service listen address")
	flags.BoolVar(&options.AllowUnauthenticated, "allow-unauthenticated", false, "allow missing bearer authentication for isolated local testing")
	flags.StringVar(&options.IdempotencyDirectory, "idempotency-dir", "data/sandbox-idempotency", "durable idempotency response directory")
	flags.StringVar(&options.BackendMode, "backend", "virtual", "execution backend: virtual or rooted")
	flags.StringVar(&options.WorkspaceRoot, "workspace-root", "", "absolute workspace root for the Linux rooted backend")
	flags.StringVar(&options.OTLPEndpoint, "otlp-endpoint", "", "optional OTLP/HTTP traces endpoint")
	flags.Float64Var(&options.TraceSampleRatio, "trace-sample-ratio", 1, "OpenTelemetry trace sample ratio in [0,1]")
	flags.Int64Var(&options.MaxBodyBytes, "max-body-bytes", 4<<20, "maximum execution request body")
	flags.Float64Var(&options.RequestsPerSecond, "requests-per-second", 20, "process-wide execution request rate")
	flags.IntVar(&options.RequestBurst, "request-burst", 40, "process-wide execution request burst")
	if err := flags.Parse(arguments); err != nil {
		return sandboxOptions{}, fmt.Errorf("parse sandbox options: %w", err)
	}
	if len(flags.Args()) != 0 {
		return sandboxOptions{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if options.Listen == "" || options.IdempotencyDirectory == "" {
		return sandboxOptions{}, fmt.Errorf("listen and idempotency-dir must be non-empty")
	}
	if options.BackendMode != "virtual" && options.BackendMode != "rooted" {
		return sandboxOptions{}, fmt.Errorf("backend must be virtual or rooted")
	}
	if options.BackendMode == "rooted" && options.WorkspaceRoot == "" {
		return sandboxOptions{}, fmt.Errorf("rooted backend requires workspace-root")
	}
	if math.IsNaN(options.TraceSampleRatio) || math.IsInf(options.TraceSampleRatio, 0) ||
		options.TraceSampleRatio < 0 || options.TraceSampleRatio > 1 {
		return sandboxOptions{}, fmt.Errorf("trace-sample-ratio must be in [0,1]")
	}
	if options.MaxBodyBytes <= 0 || math.IsNaN(options.RequestsPerSecond) ||
		math.IsInf(options.RequestsPerSecond, 0) || options.RequestsPerSecond <= 0 ||
		options.RequestBurst <= 0 {
		return sandboxOptions{}, fmt.Errorf("request limits must be positive")
	}
	return options, nil
}
