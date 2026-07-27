package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"bouncer/internal/executor"
	"bouncer/internal/sandbox"
	"bouncer/internal/telemetry"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8082", "sandbox service listen address")
	allowUnauthenticated := flag.Bool("allow-unauthenticated", false, "allow missing bearer authentication for isolated local testing")
	idempotencyDirectory := flag.String("idempotency-dir", "data/sandbox-idempotency", "durable idempotency response directory")
	backendMode := flag.String("backend", "virtual", "execution backend: virtual or rooted")
	workspaceRoot := flag.String("workspace-root", "", "absolute workspace root for the Linux rooted backend")
	otlpEndpoint := flag.String("otlp-endpoint", "", "optional OTLP/HTTP traces endpoint")
	traceSampleRatio := flag.Float64("trace-sample-ratio", 1, "OpenTelemetry trace sample ratio in [0,1]")
	flag.Parse()
	shutdownTelemetry, err := telemetry.Setup(context.Background(), telemetry.Config{
		ServiceName:    "bouncer-sandbox",
		ServiceVersion: "0.1.0",
		OTLPEndpoint:   *otlpEndpoint,
		SampleRatio:    *traceSampleRatio,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownContext); err != nil {
			log.Printf("shutdown telemetry: %v", err)
		}
	}()
	token := os.Getenv("BOUNCER_SANDBOX_TOKEN")
	if token == "" && !*allowUnauthenticated {
		log.Fatal("BOUNCER_SANDBOX_TOKEN is required unless -allow-unauthenticated is set")
	}
	store, err := sandbox.NewFileStore(*idempotencyDirectory)
	if err != nil {
		log.Fatal(err)
	}
	var backend executor.Executor = executor.Virtual{}
	if *backendMode == "rooted" {
		rooted, rootedErr := executor.NewRooted(executor.RootedConfig{Root: *workspaceRoot})
		if rootedErr != nil {
			log.Fatal(rootedErr)
		}
		defer rooted.Close()
		backend = rooted
	} else if *backendMode != "virtual" {
		log.Fatal("backend must be virtual or rooted")
	}
	handler, err := sandbox.NewHandler(sandbox.Config{
		Token:   token,
		Backend: backend,
		Store:   store,
	})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	log.Printf("bouncer reference sandbox listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
