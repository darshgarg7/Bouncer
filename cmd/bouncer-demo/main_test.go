package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDemoExercisesFiveBoundaries(t *testing.T) {
	var output bytes.Buffer
	root := filepath.Join("..", "..")
	if err := runDemo(root, &output); err != nil {
		t.Fatalf("runDemo returned %v", err)
	}
	for _, expected := range []string{
		"malformed proposal rejected",
		"READ_DENIED",
		"safe action executed",
		"tamper detected",
		"learned routing shadowed",
		"DEMO PASSED: 5/5",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("demo output does not contain %q:\n%s", expected, output.String())
		}
	}
}
