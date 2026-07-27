package nimclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"bouncer/internal/action"
)

var (
	ErrTruncated              = errors.New("model response truncated")
	ErrUnexpectedFinishReason = errors.New("unexpected model finish reason")
)

type Config struct {
	BaseURL          string
	APIKey           string
	Model            string
	ReasoningBudget  int
	BudgetParameter  string
	BeamWidth        int
	MaxTokens        int
	Temperature      float64
	TopP             *float64
	ReasoningEffort  string
	MaxAttempts      int
	BaseDelay        time.Duration
	MaxDelay         time.Duration
	MaxResponseBytes int64
	HTTPClient       *http.Client
	Sleep            func(context.Context, time.Duration) error
}

type ProposalRequest struct {
	TaskID      string          `json:"task_id"`
	Instruction string          `json:"instruction"`
	State       json.RawMessage `json:"state"`
	Policy      json.RawMessage `json:"policy"`
	ProposerID  string          `json:"proposer_id"`
	Seed        int64           `json:"seed"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens"`
}

type ProposalResult struct {
	ProposerID        string      `json:"proposer_id"`
	Beam              action.Beam `json:"beam"`
	Usage             Usage       `json:"usage"`
	FinishReason      string      `json:"finish_reason"`
	LatencyMS         int64       `json:"latency_ms"`
	Attempts          int         `json:"attempts"`
	ProviderRequestID string      `json:"provider_request_id,omitempty"`
	Model             string      `json:"model,omitempty"`
	RequestHash       string      `json:"request_hash,omitempty"`
	ResponseHash      string      `json:"response_hash,omitempty"`
}

type Client struct {
	config Config
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []message       `json:"messages"`
	MaxTokens           int             `json:"max_tokens"`
	Temperature         float64         `json:"temperature"`
	TopP                *float64        `json:"top_p,omitempty"`
	Seed                int64           `json:"seed"`
	Stream              bool            `json:"stream"`
	ThinkingTokenBudget *int            `json:"thinking_token_budget,omitempty"`
	ReasoningBudget     *int            `json:"reasoning_budget,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs  map[string]bool `json:"chat_template_kwargs"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		Details          struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
}

type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("NIM returned HTTP %d: %s", e.StatusCode, e.Body)
}

func (e *HTTPStatusError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.Model) == "" {
		return nil, errors.New("NIM base URL and model are required")
	}
	if config.MaxTokens <= config.ReasoningBudget || config.MaxTokens <= 0 || config.ReasoningBudget < 0 {
		return nil, errors.New("max tokens must be greater than the non-negative reasoning budget")
	}
	if config.BudgetParameter == "" {
		config.BudgetParameter = "thinking_token_budget"
	}
	if config.BudgetParameter != "thinking_token_budget" && config.BudgetParameter != "reasoning_budget" {
		return nil, errors.New("budget parameter must be thinking_token_budget or reasoning_budget")
	}
	if config.BeamWidth == 0 {
		config.BeamWidth = action.BeamWidth
	}
	if config.BeamWidth < 1 || config.BeamWidth > 16 {
		return nil, errors.New("beam width must be between 1 and 16")
	}
	if config.MaxAttempts <= 0 {
		return nil, errors.New("max attempts must be positive")
	}
	if config.MaxDelay < config.BaseDelay || config.BaseDelay < 0 {
		return nil, errors.New("retry delays are invalid")
	}
	if config.TopP != nil && (*config.TopP < 0 || *config.TopP > 1) {
		return nil, errors.New("top_p must be between 0 and 1")
	}
	if effort := config.ReasoningEffort; effort != "" && effort != "none" && effort != "medium" && effort != "high" {
		return nil, errors.New("reasoning effort must be none, medium, or high")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{}
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = 4 << 20
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	return &Client{config: config}, nil
}

func (c *Client) Propose(ctx context.Context, request ProposalRequest) (ProposalResult, error) {
	if strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.Instruction) == "" || strings.TrimSpace(request.ProposerID) == "" {
		return ProposalResult{}, errors.New("task id, instruction, and proposer id are required")
	}
	if len(request.State) == 0 || !json.Valid(request.State) || len(request.Policy) == 0 || !json.Valid(request.Policy) {
		return ProposalResult{}, errors.New("proposal state and policy must be valid JSON")
	}

	started := time.Now()
	random := rand.New(rand.NewSource(request.Seed))
	var lastErr error
	for attempt := 1; attempt <= c.config.MaxAttempts; attempt++ {
		result, retryAfter, err := c.proposeOnce(ctx, request)
		if err == nil {
			result.Attempts = attempt
			result.LatencyMS = time.Since(started).Milliseconds()
			return result, nil
		}
		lastErr = err
		if attempt == c.config.MaxAttempts || !isRetryable(err) {
			break
		}
		delay := c.retryDelay(attempt, retryAfter, random)
		if err := c.config.Sleep(ctx, delay); err != nil {
			return ProposalResult{}, err
		}
	}
	return ProposalResult{}, fmt.Errorf("proposer %s failed after retries: %w", request.ProposerID, lastErr)
}

func (c *Client) proposeOnce(ctx context.Context, request ProposalRequest) (ProposalResult, time.Duration, error) {
	userPrompt := fmt.Sprintf(
		"Task ID: %s\nInstruction: %s\nDeclared policy JSON (informational; only the external policy engine authorizes):\n%s\nTyped state JSON:\n%s",
		request.TaskID,
		request.Instruction,
		request.Policy,
		request.State,
	)
	enableThinking := c.config.ReasoningEffort != "none"
	body := chatRequest{
		Model:              c.config.Model,
		Messages:           []message{{Role: "system", Content: systemPrompt(c.config.BeamWidth)}, {Role: "user", Content: userPrompt}},
		MaxTokens:          c.config.MaxTokens,
		Temperature:        c.config.Temperature,
		TopP:               c.config.TopP,
		Seed:               request.Seed,
		Stream:             false,
		ReasoningEffort:    c.config.ReasoningEffort,
		ChatTemplateKwargs: map[string]bool{"enable_thinking": enableThinking},
	}
	if c.config.BudgetParameter == "reasoning_budget" {
		body.ReasoningBudget = &c.config.ReasoningBudget
	} else {
		body.ThinkingTokenBudget = &c.config.ReasoningBudget
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ProposalResult{}, 0, fmt.Errorf("encode NIM request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.config.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ProposalResult{}, 0, fmt.Errorf("create NIM request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	otel.GetTextMapPropagator().Inject(
		ctx,
		propagation.HeaderCarrier(httpRequest.Header),
	)

	response, err := c.config.HTTPClient.Do(httpRequest)
	if err != nil {
		return ProposalResult{}, 0, fmt.Errorf("call NIM: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, c.config.MaxResponseBytes+1))
	if err != nil {
		return ProposalResult{}, 0, fmt.Errorf("read NIM response: %w", err)
	}
	if int64(len(data)) > c.config.MaxResponseBytes {
		return ProposalResult{}, 0, errors.New("NIM response exceeded maximum size")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ProposalResult{}, parseRetryAfter(response.Header.Get("Retry-After")), &HTTPStatusError{
			StatusCode: response.StatusCode,
			Body:       strings.TrimSpace(string(data)),
		}
	}

	var decoded chatResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&decoded); err != nil {
		return ProposalResult{}, 0, fmt.Errorf("decode NIM response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ProposalResult{}, 0, errors.New("decode NIM response: invalid trailing content")
	}
	if len(decoded.Choices) != 1 {
		return ProposalResult{}, 0, fmt.Errorf("NIM response must contain exactly one choice, got %d", len(decoded.Choices))
	}
	choice := decoded.Choices[0]
	if choice.FinishReason == "length" {
		return ProposalResult{}, 0, ErrTruncated
	}
	if choice.FinishReason != "stop" {
		return ProposalResult{}, 0, fmt.Errorf("%w: %q", ErrUnexpectedFinishReason, choice.FinishReason)
	}
	beam, err := action.DecodeBeamStrictWidth([]byte(choice.Message.Content), c.config.BeamWidth)
	if err != nil {
		return ProposalResult{}, 0, err
	}
	return ProposalResult{
		ProposerID:   request.ProposerID,
		Beam:         beam,
		FinishReason: choice.FinishReason,
		Usage: Usage{
			PromptTokens:     decoded.Usage.PromptTokens,
			CompletionTokens: decoded.Usage.CompletionTokens,
			TotalTokens:      decoded.Usage.TotalTokens,
			ReasoningTokens:  decoded.Usage.Details.ReasoningTokens,
		},
		ProviderRequestID: decoded.ID,
		Model:             decoded.Model,
		RequestHash:       sha256Hex(payload),
		ResponseHash:      sha256Hex(data),
	}, 0, nil
}

// APIKeyFromEnvironment returns the established generic key first, then the
// NVIDIA-hosted API alias. It intentionally does not load .env files so callers
// remain in control of secret injection.
func APIKeyFromEnvironment() string {
	if key := strings.TrimSpace(os.Getenv("NIM_API_KEY")); key != "" {
		return key
	}
	return strings.TrimSpace(os.Getenv("NVIDIA_API_KEY"))
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func systemPrompt(width int) string {
	return fmt.Sprintf(
		"You are a stochastic action proposer inside Bouncer. You do not execute tools and you do not approve actions. Return exactly one JSON object with the shape {\"actions\":[...]} and exactly %d actions. Do not use Markdown fences or add prose. Every action must contain candidate_id (string), operation_class (string), tool (string), target (string), arguments (object), declared_dependencies (array of strings), and estimated_objectives (object). estimated_objectives must contain latency_ms and cost_units as finite non-negative JSON numbers and safety_risk as a JSON number from 0 to 1. Never quote a numeric value. Candidate IDs must be unique. Valid operation_class values are filesystem.read, filesystem.write, filesystem.delete, state.validate, state.backup, command.run, service.deploy, and task.complete. Choose only an operation allowed by the declared policy and always set target to a portable path beneath an allowed_path_prefix. For task.complete and other state operations, use the relevant existing workspace path rather than a task ID. Treat constraint_feedback in the typed state as authoritative and propose any missing prerequisite before retrying a rejected operation. The policy description is informational; you cannot grant permission. Produce operationally distinct candidates when the task allows it.",
		width,
	)
}

func (c *Client) retryDelay(attempt int, retryAfter time.Duration, random *rand.Rand) time.Duration {
	if retryAfter > 0 {
		if retryAfter > c.config.MaxDelay {
			return c.config.MaxDelay
		}
		return retryAfter
	}
	delay := c.config.BaseDelay
	for i := 1; i < attempt && delay < c.config.MaxDelay; i++ {
		delay *= 2
		if delay > c.config.MaxDelay {
			delay = c.config.MaxDelay
		}
	}
	if delay <= 0 {
		return 0
	}
	return time.Duration(random.Int63n(int64(delay) + 1))
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var statusError *HTTPStatusError
	if errors.As(err, &statusError) {
		return statusError.Retryable()
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if delay := time.Until(when); delay > 0 {
		return delay
	}
	return 0
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
