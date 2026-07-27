package sandbox

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"bouncer/internal/executor"
)

var ErrExecutionIndeterminate = errors.New(
	"idempotency key has an indeterminate in-progress execution",
)

type ResponseStore interface {
	Get(context.Context, string) (executor.ExecutionResponse, bool, error)
	Claim(context.Context, string) (bool, error)
	Put(context.Context, string, executor.ExecutionResponse) error
}

type MemoryStore struct {
	mutex     sync.RWMutex
	responses map[string]executor.ExecutionResponse
	claims    map[string]struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		responses: map[string]executor.ExecutionResponse{},
		claims:    map[string]struct{}{},
	}
}

func (s *MemoryStore) Get(ctx context.Context, key string) (executor.ExecutionResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return executor.ExecutionResponse{}, false, err
	}
	if err := validateKey(key); err != nil {
		return executor.ExecutionResponse{}, false, err
	}
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	response, exists := s.responses[key]
	if exists {
		return response, true, nil
	}
	if _, claimed := s.claims[key]; claimed {
		return executor.ExecutionResponse{}, false, ErrExecutionIndeterminate
	}
	return executor.ExecutionResponse{}, false, nil
}

func (s *MemoryStore) Claim(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateKey(key); err != nil {
		return false, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists := s.responses[key]; exists {
		return false, nil
	}
	if _, exists := s.claims[key]; exists {
		return false, nil
	}
	s.claims[key] = struct{}{}
	return true, nil
}

func (s *MemoryStore) Put(ctx context.Context, key string, response executor.ExecutionResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStoredResponse(key, response); err != nil {
		return err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if existing, exists := s.responses[key]; exists {
		if !responsesEqual(existing, response) {
			return errors.New("idempotency key collision with a different response")
		}
		return nil
	}
	if _, claimed := s.claims[key]; !claimed {
		return errors.New("idempotency response cannot complete an unclaimed key")
	}
	s.responses[key] = response
	return nil
}

type FileStore struct {
	directory string
	mutex     sync.Mutex
}

func NewFileStore(directory string) (*FileStore, error) {
	if directory == "" {
		return nil, errors.New("idempotency directory is required")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve idempotency directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create idempotency directory: %w", err)
	}
	return &FileStore{directory: absolute}, nil
}

func (s *FileStore) Get(ctx context.Context, key string) (executor.ExecutionResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return executor.ExecutionResponse{}, false, err
	}
	if err := validateKey(key); err != nil {
		return executor.ExecutionResponse{}, false, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.getLocked(key)
}

func (s *FileStore) Claim(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateKey(key); err != nil {
		return false, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if _, exists, err := s.getResponseLocked(key); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	claimPath := filepath.Join(s.directory, key+".claim")
	claim, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create idempotency claim: %w", err)
	}
	if _, err := claim.WriteString(key + "\n"); err != nil {
		claim.Close()
		return false, fmt.Errorf("write idempotency claim: %w", err)
	}
	if err := claim.Sync(); err != nil {
		claim.Close()
		return false, fmt.Errorf("sync idempotency claim: %w", err)
	}
	if err := claim.Close(); err != nil {
		return false, fmt.Errorf("close idempotency claim: %w", err)
	}
	if err := syncDirectory(s.directory); err != nil {
		return false, err
	}
	return true, nil
}

func (s *FileStore) Put(ctx context.Context, key string, response executor.ExecutionResponse) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStoredResponse(key, response); err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	encoded = append(encoded, '\n')
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if existing, exists, getErr := s.getResponseLocked(key); getErr != nil {
		return getErr
	} else if exists {
		if !responsesEqual(existing, response) {
			return errors.New("idempotency key collision with a different response")
		}
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.directory, key+".claim")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("idempotency response cannot complete an unclaimed key")
		}
		return fmt.Errorf("inspect idempotency claim: %w", err)
	}
	temporary, err := os.CreateTemp(s.directory, "pending-response-*")
	if err != nil {
		return fmt.Errorf("create idempotency record: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure idempotency record: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		return fmt.Errorf("write idempotency record: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync idempotency record: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close idempotency record: %w", err)
	}
	finalPath := filepath.Join(s.directory, key+".json")
	if err := os.Link(temporaryPath, finalPath); err != nil {
		if existing, exists, getErr := s.getResponseLocked(key); getErr == nil &&
			exists && responsesEqual(existing, response) {
			return nil
		}
		return fmt.Errorf("commit idempotency record: %w", err)
	}
	return syncDirectory(s.directory)
}

func (s *FileStore) getLocked(key string) (executor.ExecutionResponse, bool, error) {
	response, exists, err := s.getResponseLocked(key)
	if err != nil || exists {
		return response, exists, err
	}
	if _, err := os.Stat(filepath.Join(s.directory, key+".claim")); err == nil {
		return executor.ExecutionResponse{}, false, ErrExecutionIndeterminate
	} else if !errors.Is(err, os.ErrNotExist) {
		return executor.ExecutionResponse{}, false, fmt.Errorf("inspect idempotency claim: %w", err)
	}
	return executor.ExecutionResponse{}, false, nil
}

func (s *FileStore) getResponseLocked(key string) (executor.ExecutionResponse, bool, error) {
	data, err := os.ReadFile(filepath.Join(s.directory, key+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return executor.ExecutionResponse{}, false, nil
	}
	if err != nil {
		return executor.ExecutionResponse{}, false, fmt.Errorf("read idempotency record: %w", err)
	}
	var response executor.ExecutionResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return executor.ExecutionResponse{}, false, fmt.Errorf("decode idempotency record: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return executor.ExecutionResponse{}, false, errors.New(
			"decode idempotency record: invalid trailing content",
		)
	}
	if err := validateStoredResponse(key, response); err != nil {
		return executor.ExecutionResponse{}, false, err
	}
	return response, true, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open idempotency directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync idempotency directory: %w", err)
	}
	return nil
}

func validateStoredResponse(key string, response executor.ExecutionResponse) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if response.SchemaVersion != "0.1.0" || response.IdempotencyKey != key {
		return errors.New("stored response protocol or idempotency key mismatch")
	}
	return nil
}

func validateKey(key string) error {
	if len(key) != 64 {
		return errors.New("idempotency key must be lowercase SHA-256 hex")
	}
	decoded, err := hex.DecodeString(key)
	if err != nil || hex.EncodeToString(decoded) != key {
		return errors.New("idempotency key must be lowercase SHA-256 hex")
	}
	return nil
}

func responsesEqual(left, right executor.ExecutionResponse) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
