package sandbox

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"bouncer/internal/executor"
)

type Config struct {
	Token             string
	Backend           executor.Executor
	MaxBodyBytes      int64
	RequestsPerSecond float64
	Burst             int
	Store             ResponseStore
	Metrics           *Metrics
}

func NewHandler(config Config) (http.Handler, error) {
	if config.Backend == nil {
		return nil, errors.New("sandbox backend is required")
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 4 << 20
	}
	if config.RequestsPerSecond <= 0 {
		config.RequestsPerSecond = 20
	}
	if config.Burst <= 0 {
		config.Burst = 40
	}
	if config.Store == nil {
		config.Store = NewMemoryStore()
	}
	if config.Metrics == nil {
		config.Metrics = NewMetrics()
	}
	var executionMutex sync.Mutex
	limiter := newTokenBucket(config.RequestsPerSecond, config.Burst)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestContext := otel.GetTextMapPropagator().Extract(
			request.Context(),
			propagation.HeaderCarrier(request.Header),
		)
		requestContext, span := otel.Tracer("bouncer/sandbox").Start(
			requestContext,
			"sandbox.request",
			trace.WithAttributes(
				attribute.String("http.request.method", request.Method),
				attribute.String("url.path", request.URL.Path),
			),
		)
		defer span.End()
		request = request.WithContext(requestContext)
		switch request.URL.Path {
		case "/metrics":
			if request.Method != http.MethodGet {
				writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			config.Metrics.ServeHTTP(writer, request)
			return
		case "/health/live", "/health/ready":
			if request.Method != http.MethodGet {
				writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
			return
		case "/v1/execute":
		default:
			writeError(writer, http.StatusNotFound, "not found")
			return
		}
		if request.Method != http.MethodPost {
			writeError(writer, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		finishMetrics := config.Metrics.begin()
		defer finishMetrics()
		if config.Token != "" && !validBearerToken(request.Header.Get("Authorization"), config.Token) {
			writeError(writer, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !limiter.Allow(time.Now()) {
			writer.Header().Set("Retry-After", "1")
			writeError(writer, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		data, err := io.ReadAll(io.LimitReader(request.Body, config.MaxBodyBytes+1))
		if err != nil {
			config.Metrics.errors.Add(1)
			writeError(writer, http.StatusBadRequest, "read request")
			return
		}
		if int64(len(data)) > config.MaxBodyBytes {
			writeError(writer, http.StatusRequestEntityTooLarge, "request too large")
			return
		}
		var executionRequest executor.ExecutionRequest
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&executionRequest); err != nil {
			writeError(writer, http.StatusBadRequest, "invalid request")
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			writeError(writer, http.StatusBadRequest, "invalid trailing content")
			return
		}
		if request.Header.Get("Idempotency-Key") != executionRequest.IdempotencyKey {
			writeError(writer, http.StatusBadRequest, "idempotency header mismatch")
			return
		}
		if err := executor.ValidateExecutionRequest(executionRequest); err != nil {
			writeError(writer, http.StatusBadRequest, err.Error())
			return
		}
		executionMutex.Lock()
		defer executionMutex.Unlock()
		cached, exists, err := config.Store.Get(request.Context(), executionRequest.IdempotencyKey)
		if err != nil {
			config.Metrics.errors.Add(1)
			if errors.Is(err, ErrExecutionIndeterminate) {
				writeError(
					writer,
					http.StatusConflict,
					"execution outcome is indeterminate; manual reconciliation required",
				)
				return
			}
			writeError(writer, http.StatusInternalServerError, "idempotency store unavailable")
			return
		}
		if exists {
			config.Metrics.replays.Add(1)
			writer.Header().Set("Idempotency-Replayed", "true")
			writeJSON(writer, http.StatusOK, cached)
			return
		}
		claimed, err := config.Store.Claim(request.Context(), executionRequest.IdempotencyKey)
		if err != nil {
			config.Metrics.errors.Add(1)
			writeError(writer, http.StatusInternalServerError, "idempotency claim unavailable")
			return
		}
		if !claimed {
			cached, exists, err = config.Store.Get(request.Context(), executionRequest.IdempotencyKey)
			if err == nil && exists {
				config.Metrics.replays.Add(1)
				writer.Header().Set("Idempotency-Replayed", "true")
				writeJSON(writer, http.StatusOK, cached)
				return
			}
			config.Metrics.errors.Add(1)
			writeError(
				writer,
				http.StatusConflict,
				"execution outcome is indeterminate; manual reconciliation required",
			)
			return
		}
		state := executionRequest.State
		outcome, err := config.Backend.Execute(
			request.Context(),
			&state,
			executionRequest.Policy,
			executionRequest.Candidate,
		)
		if err != nil {
			config.Metrics.errors.Add(1)
			writeError(writer, http.StatusUnprocessableEntity, err.Error())
			return
		}
		config.Metrics.executions.Add(1)
		executionResponse := executor.ExecutionResponse{
			SchemaVersion:  "0.1.0",
			IdempotencyKey: executionRequest.IdempotencyKey,
			State:          state,
			Outcome:        outcome,
		}
		if err := config.Store.Put(request.Context(), executionRequest.IdempotencyKey, executionResponse); err != nil {
			config.Metrics.errors.Add(1)
			writeError(writer, http.StatusInternalServerError, "persist idempotency response")
			return
		}
		writeJSON(writer, http.StatusOK, executionResponse)
	}), nil
}

type tokenBucket struct {
	mutex  sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newTokenBucket(requestsPerSecond float64, burst int) *tokenBucket {
	now := time.Now()
	return &tokenBucket{
		rate:   requestsPerSecond,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now,
	}
}

func (l *tokenBucket) Allow(now time.Time) bool {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	elapsed := now.Sub(l.last).Seconds()
	if elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func validBearerToken(header, token string) bool {
	want := "Bearer " + token
	if len(header) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(want)) == 1
}

func writeError(writer http.ResponseWriter, status int, message string) {
	writeJSON(writer, status, map[string]string{"error": message})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(writer, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(data)
}
