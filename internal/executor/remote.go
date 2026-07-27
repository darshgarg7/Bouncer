package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

type ExecutionRequest struct {
	SchemaVersion  string           `json:"schema_version"`
	IdempotencyKey string           `json:"idempotency_key"`
	State          benchmark.State  `json:"state"`
	Policy         benchmark.Policy `json:"policy"`
	Candidate      action.Candidate `json:"candidate"`
}

type ExecutionResponse struct {
	SchemaVersion  string          `json:"schema_version"`
	IdempotencyKey string          `json:"idempotency_key"`
	State          benchmark.State `json:"state"`
	Outcome        Outcome         `json:"outcome"`
}

type RemoteConfig struct {
	BaseURL              string
	Token                string
	AllowUnauthenticated bool
	AllowInsecureHTTP    bool
	HTTPClient           *http.Client
	MaxResponseBytes     int64
}

type Remote struct {
	config RemoteConfig
}

func NewRemote(config RemoteConfig) (*Remote, error) {
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("sandbox base URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && !(config.AllowInsecureHTTP && parsed.Scheme == "http") {
		return nil, errors.New("sandbox requires HTTPS unless insecure HTTP is explicitly enabled")
	}
	if config.Token == "" && !config.AllowUnauthenticated {
		return nil, errors.New("sandbox bearer token is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 4 << 20
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	return &Remote{config: config}, nil
}

func (r *Remote) Execute(
	ctx context.Context,
	state *benchmark.State,
	policy benchmark.Policy,
	candidate action.Candidate,
) (Outcome, error) {
	if state == nil {
		return Outcome{}, errors.New("executor state is required")
	}
	key, err := ComputeIdempotencyKey(*state, policy, candidate)
	if err != nil {
		return Outcome{}, err
	}
	requestBody := ExecutionRequest{
		SchemaVersion:  "0.1.0",
		IdempotencyKey: key,
		State:          *state,
		Policy:         policy,
		Candidate:      candidate,
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return Outcome{}, fmt.Errorf("encode sandbox request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		r.config.BaseURL+"/v1/execute",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("create sandbox request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	if r.config.Token != "" {
		request.Header.Set("Authorization", "Bearer "+r.config.Token)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := r.config.HTTPClient.Do(request)
	if err != nil {
		return Outcome{}, fmt.Errorf("call sandbox: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, r.config.MaxResponseBytes+1))
	if err != nil {
		return Outcome{}, fmt.Errorf("read sandbox response: %w", err)
	}
	if int64(len(data)) > r.config.MaxResponseBytes {
		return Outcome{}, errors.New("sandbox response exceeded maximum size")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Outcome{}, fmt.Errorf(
			"sandbox returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(data)),
		)
	}
	var decoded ExecutionResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Outcome{}, fmt.Errorf("decode sandbox response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Outcome{}, errors.New("decode sandbox response: invalid trailing content")
	}
	if decoded.SchemaVersion != "0.1.0" || decoded.IdempotencyKey != key {
		return Outcome{}, errors.New("sandbox response protocol or idempotency key mismatch")
	}
	if err := validateRemoteTransition(*state, decoded.State, candidate, decoded.Outcome); err != nil {
		return Outcome{}, err
	}
	*state = decoded.State
	return decoded.Outcome, nil
}

func ComputeIdempotencyKey(
	state benchmark.State,
	policy benchmark.Policy,
	candidate action.Candidate,
) (string, error) {
	payload := struct {
		State     benchmark.State  `json:"state"`
		Policy    benchmark.Policy `json:"policy"`
		Candidate action.Candidate `json:"candidate"`
	}{State: state, Policy: policy, Candidate: candidate}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode sandbox idempotency payload: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateExecutionRequest(request ExecutionRequest) error {
	if request.SchemaVersion != "0.1.0" {
		return errors.New("unsupported sandbox protocol version")
	}
	if err := request.Candidate.Validate(); err != nil {
		return fmt.Errorf("invalid sandbox candidate: %w", err)
	}
	expected, err := ComputeIdempotencyKey(request.State, request.Policy, request.Candidate)
	if err != nil {
		return err
	}
	if request.IdempotencyKey != expected {
		return errors.New("sandbox idempotency key mismatch")
	}
	if err := validateRequestedPolicy(request.State, request.Policy, request.Candidate); err != nil {
		return err
	}
	return nil
}

func validateRequestedPolicy(
	state benchmark.State,
	policy benchmark.Policy,
	candidate action.Candidate,
) error {
	operationAllowed := false
	for _, operation := range policy.AllowedOperationClasses {
		if operation == candidate.OperationClass {
			operationAllowed = true
			break
		}
	}
	if !operationAllowed {
		return fmt.Errorf("sandbox policy does not allow operation %q", candidate.OperationClass)
	}
	target, ok := normalizeVirtualPath(candidate.Target)
	if !ok {
		return errors.New("sandbox candidate target is invalid")
	}
	pathAllowed := false
	for _, prefix := range policy.AllowedPathPrefixes {
		if pathWithin(target, prefix) {
			pathAllowed = true
			break
		}
	}
	if !pathAllowed {
		return fmt.Errorf("sandbox policy does not allow target %q", target)
	}
	if candidate.OperationClass == "filesystem.read" {
		for _, denied := range policy.DeniedReadPaths {
			if pathWithin(target, denied) {
				return fmt.Errorf("sandbox policy denies reading target %q", target)
			}
		}
	} else {
		for _, protected := range policy.ProtectedPaths {
			if pathWithin(target, protected) {
				return fmt.Errorf("sandbox policy protects target %q", target)
			}
		}
	}
	mutating := candidate.OperationClass == "filesystem.write" ||
		candidate.OperationClass == "filesystem.delete" ||
		candidate.OperationClass == "service.deploy"
	if mutating && state.MutationCount >= policy.MaxMutations {
		return fmt.Errorf("sandbox mutation limit %d exhausted", policy.MaxMutations)
	}
	return nil
}

func normalizeVirtualPath(value string) (string, bool) {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return "", false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return "", false
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned != value {
		return "", false
	}
	return cleaned, true
}

func pathWithin(target, configured string) bool {
	prefix, ok := normalizeVirtualPath(strings.TrimSuffix(configured, "/"))
	if !ok {
		return false
	}
	return target == prefix || strings.HasPrefix(target, prefix+"/")
}

func validateRemoteTransition(
	before benchmark.State,
	after benchmark.State,
	candidate action.Candidate,
	outcome Outcome,
) error {
	expectedState := cloneState(before)
	expectedOutcome, err := (Virtual{}).Execute(
		context.Background(),
		&expectedState,
		benchmark.Policy{MaxMutations: before.MutationCount + 1},
		candidate,
	)
	if err != nil {
		return fmt.Errorf("compute expected sandbox transition: %w", err)
	}
	if !reflect.DeepEqual(after, expectedState) {
		return errors.New("sandbox response state does not match the deterministic transition contract")
	}
	if !reflect.DeepEqual(outcome, expectedOutcome) {
		return errors.New("sandbox response outcome does not match the deterministic transition contract")
	}
	return nil
}

func cloneState(state benchmark.State) benchmark.State {
	clone := state
	clone.CompletedOperations = append([]string(nil), state.CompletedOperations...)
	clone.ConstraintFeedback = append([]string(nil), state.ConstraintFeedback...)
	clone.Files = make(map[string]string, len(state.Files))
	for filePath, content := range state.Files {
		clone.Files[filePath] = content
	}
	return clone
}
