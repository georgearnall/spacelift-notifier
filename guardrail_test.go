package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mutateCallPattern is built at runtime rather than written as a literal
// in this file, so this guardrail test doesn't trip over its own source
// when it scans the repo below.
var mutateCallPattern = "." + "Mutate("

// TestNoMutationCallsAnywhere is a guardrail: spacelift-notifier is
// read-only by design - it must never call the Spacelift SDK's Mutate
// method (confirm/discard/approve are all mutations). If this test
// fails, something added a mutation call and needs to be reconsidered
// before merging: the whole point of this tool is that it only surfaces
// and links, and the user does any confirming themselves.
func TestNoMutationCallsAnywhere(t *testing.T) {
	var offenders []string

	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), mutateCallPattern) {
			offenders = append(offenders, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking repo: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("found a Mutate(...) call in: %v - this tool must stay read-only", offenders)
	}
}
