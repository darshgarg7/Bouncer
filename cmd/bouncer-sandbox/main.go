package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bouncer/internal/executor"
	"bouncer/internal/sandbox"
	"bouncer/internal/telemetry"
)

func main() {
	options, err := parseOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	rootContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	shutdownTelemetry, err := telemetry.Setup(rootContext, telemetry.Config{
		ServiceName:    "bouncer-sandbox",
		ServiceVersion: "0.1.0",
		OTLPEndpoint:   options.OTLPEndpoint,
		SampleRatio:    options.TraceSampleRatio,
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
	if token == "" && !options.AllowUnauthenticated {
		log.Fatal("BOUNCER_SANDBOX_TOKEN is required unless -allow-unauthenticated is set")
	}
	store, err := sandbox.NewFileStore(options.IdempotencyDirectory)
	if err != nil {
		log.Fatal(err)
	}
	var backend executor.Executor = executor.Virtual{}
	if options.BackendMode == "rooted" {
		rooted, rootedErr := executor.NewRooted(executor.RootedConfig{Root: options.WorkspaceRoot})
		if rootedErr != nil {
			log.Fatal(rootedErr)
		}
		defer rooted.Close()
		backend = rooted
	} else if options.BackendMode != "virtual" {
		log.Fatal("backend must be virtual or rooted")
	}
	handler, err := sandbox.NewHandler(sandbox.Config{
		Token:             token,
		Backend:           backend,
		MaxBodyBytes:      options.MaxBodyBytes,
		RequestsPerSecond: options.RequestsPerSecond,
		Burst:             options.RequestBurst,
		Store:             store,
	})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Addr:              options.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	shutdownComplete := make(chan struct{})
	go func() {
		<-rootContext.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("graceful HTTP shutdown: %v", err)
		}
		close(shutdownComplete)
	}()
	log.Printf("bouncer reference sandbox listening on %s", options.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
	if rootContext.Err() != nil {
		<-shutdownComplete
	}
}
