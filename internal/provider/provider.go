package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"bouncer/internal/nimclient"
)

const (
	KindOpenAICompatible = "openai-compatible"
	KindRecorded         = "recorded"
)

type Provider interface {
	Propose(context.Context, nimclient.ProposalRequest) (nimclient.ProposalResult, error)
}

type Config struct {
	Kind       string
	HTTP       nimclient.Config
	ReplayPath string
	BeamWidth  int
}

func New(config Config) (Provider, error) {
	switch config.Kind {
	case KindOpenAICompatible:
		return nimclient.New(config.HTTP)
	case KindRecorded:
		return LoadReplay(config.ReplayPath, config.BeamWidth)
	default:
		return nil, fmt.Errorf("unsupported provider %q", config.Kind)
	}
}

type ReplayDocument struct {
	SchemaVersion string         `json:"schema_version"`
	Records       []ReplayRecord `json:"records"`
}

type ReplayRecord struct {
	Request nimclient.ProposalRequest `json:"request"`
	Result  nimclient.ProposalResult  `json:"result"`
}

type Replay struct {
	records map[string]ReplayRecord
}

func LoadReplay(path string, beamWidth int) (*Replay, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("recorded provider requires a replay path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider replay: %w", err)
	}
	var document ReplayDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode provider replay: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode provider replay: invalid trailing content")
	}
	if document.SchemaVersion != "0.1.0" || len(document.Records) == 0 {
		return nil, errors.New("provider replay requires schema_version 0.1.0 and at least one record")
	}
	if beamWidth < 1 || beamWidth > 16 {
		return nil, errors.New("provider replay beam width must be between 1 and 16")
	}
	replay := &Replay{records: make(map[string]ReplayRecord, len(document.Records))}
	for index, record := range document.Records {
		if err := validateRequest(record.Request); err != nil {
			return nil, fmt.Errorf("provider replay record %d request: %w", index, err)
		}
		if record.Result.ProposerID != record.Request.ProposerID {
			return nil, fmt.Errorf("provider replay record %d proposer mismatch", index)
		}
		if record.Result.FinishReason != "stop" {
			return nil, fmt.Errorf("provider replay record %d finish reason must be stop", index)
		}
		if err := record.Result.Beam.ValidateWidth(beamWidth); err != nil {
			return nil, fmt.Errorf("provider replay record %d: %w", index, err)
		}
		key := replayKey(record.Request)
		if _, exists := replay.records[key]; exists {
			return nil, fmt.Errorf("provider replay record %d duplicates request key", index)
		}
		replay.records[key] = record
	}
	return replay, nil
}

func (r *Replay) Propose(
	ctx context.Context,
	request nimclient.ProposalRequest,
) (nimclient.ProposalResult, error) {
	if err := ctx.Err(); err != nil {
		return nimclient.ProposalResult{}, err
	}
	if err := validateRequest(request); err != nil {
		return nimclient.ProposalResult{}, err
	}
	record, exists := r.records[replayKey(request)]
	if !exists {
		return nimclient.ProposalResult{}, errors.New("recorded provider has no exact request key")
	}
	requested, err := json.Marshal(request)
	if err != nil {
		return nimclient.ProposalResult{}, err
	}
	recorded, err := json.Marshal(record.Request)
	if err != nil {
		return nimclient.ProposalResult{}, err
	}
	if !bytes.Equal(requested, recorded) {
		return nimclient.ProposalResult{}, errors.New("recorded provider request content mismatch")
	}
	return record.Result, nil
}

func validateRequest(request nimclient.ProposalRequest) error {
	if strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.Instruction) == "" ||
		strings.TrimSpace(request.ProposerID) == "" {
		return errors.New("task id, instruction, and proposer id are required")
	}
	if len(request.State) == 0 || !json.Valid(request.State) || len(request.Policy) == 0 || !json.Valid(request.Policy) {
		return errors.New("proposal state and policy must be valid JSON")
	}
	return nil
}

func replayKey(request nimclient.ProposalRequest) string {
	return fmt.Sprintf("%s\x00%s\x00%d", request.TaskID, request.ProposerID, request.Seed)
}
