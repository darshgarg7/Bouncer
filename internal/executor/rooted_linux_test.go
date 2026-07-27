//go:build linux

package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bouncer/internal/action"
	"bouncer/internal/benchmark"
)

func TestRootedWriteAndDelete(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootPath, "workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	rooted, err := NewRooted(RootedConfig{Root: rootPath})
	if err != nil {
		t.Fatal(err)
	}
	defer rooted.Close()
	state := benchmark.State{Files: map[string]string{}, CompletedOperations: []string{}}
	policy := rootedPolicy()
	write := rootedCandidate("filesystem.write", "workspace/file.txt")
	write.Arguments = map[string]any{"content": "hello"}
	outcome, err := rooted.Execute(context.Background(), &state, policy, write)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(rootPath, "workspace", "file.txt"))
	if err != nil || string(data) != "hello" || !outcome.Mutation {
		t.Fatalf("data=%q outcome=%+v error=%v", data, outcome, err)
	}
	remove := rootedCandidate("filesystem.delete", "workspace/file.txt")
	if _, err := rooted.Execute(context.Background(), &state, policy, remove); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootPath, "workspace", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file stat error: %v", err)
	}
}

func TestRootedDeniesSymlinkHardlinkAndTraversal(t *testing.T) {
	rootPath := t.TempDir()
	workspace := filepath.Join(rootPath, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link.txt")); err != nil {
		t.Fatal(err)
	}
	hardlinkSource := filepath.Join(workspace, "source.txt")
	if err := os.WriteFile(hardlinkSource, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(hardlinkSource, filepath.Join(workspace, "hardlink.txt")); err != nil {
		t.Fatal(err)
	}
	rooted, err := NewRooted(RootedConfig{Root: rootPath})
	if err != nil {
		t.Fatal(err)
	}
	defer rooted.Close()
	for name, target := range map[string]string{
		"symlink":   "workspace/link.txt",
		"hardlink":  "workspace/hardlink.txt",
		"traversal": "workspace/../outside.txt",
	} {
		t.Run(name, func(t *testing.T) {
			state := benchmark.State{Files: map[string]string{target: "old"}}
			candidate := rootedCandidate("filesystem.write", target)
			candidate.Arguments = map[string]any{"content": "tampered"}
			if _, err := rooted.Execute(context.Background(), &state, rootedPolicy(), candidate); err == nil {
				t.Fatal("rooted executor accepted escape target")
			}
		})
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "secret" {
		t.Fatalf("outside content=%q error=%v", data, err)
	}
	data, err = os.ReadFile(hardlinkSource)
	if err != nil || string(data) != "original" {
		t.Fatalf("hardlink source content=%q error=%v", data, err)
	}
}

func TestRootedRejectsUnrestrictedCommands(t *testing.T) {
	rootPath := t.TempDir()
	rooted, err := NewRooted(RootedConfig{Root: rootPath})
	if err != nil {
		t.Fatal(err)
	}
	defer rooted.Close()
	state := benchmark.State{Files: map[string]string{}}
	_, err = rooted.Execute(
		context.Background(),
		&state,
		rootedPolicy(),
		rootedCandidate("command.run", "workspace"),
	)
	if err == nil || !strings.Contains(err.Error(), "does not expose") {
		t.Fatalf("command execution returned %v", err)
	}
}

func rootedPolicy() benchmark.Policy {
	return benchmark.Policy{
		AllowedOperationClasses: []string{
			"filesystem.read",
			"filesystem.write",
			"filesystem.delete",
			"command.run",
		},
		AllowedPathPrefixes: []string{"workspace"},
		MaxMutations:        10,
	}
}

func rootedCandidate(operation, target string) action.Candidate {
	return action.Candidate{
		CandidateID:          "candidate-1",
		OperationClass:       operation,
		Tool:                 "rooted",
		Target:               target,
		Arguments:            map[string]any{},
		DeclaredDependencies: []string{},
		EstimatedObjectives:  action.Objectives{},
	}
}
