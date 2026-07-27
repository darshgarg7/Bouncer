package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"bouncer/internal/eventlog"
)

func main() {
	path := flag.String("event-log", "", "path to a Bouncer JSONL event log")
	expectedFinalHash := flag.String("expected-final-hash", "", "optional externally stored terminal SHA-256 hash")
	flag.Parse()
	if err := run(*path, *expectedFinalHash, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "bouncer verify log failed: %v\n", err)
		os.Exit(1)
	}
}

func run(path, expectedFinalHash string, output io.Writer) error {
	if path == "" {
		return errors.New("-event-log is required")
	}
	input, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open event log: %w", err)
	}
	defer input.Close()
	verification, err := eventlog.Verify(input)
	if err != nil {
		return fmt.Errorf("verify event log: %w", err)
	}
	if expectedFinalHash != "" && verification.FinalHash != expectedFinalHash {
		return errors.New("verify event log: final hash does not match expected external anchor")
	}
	encoded, err := json.MarshalIndent(verification, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verification: %w", err)
	}
	if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
		return fmt.Errorf("write verification: %w", err)
	}
	return nil
}
