package joblogs

import (
	"os"
	"testing"

	"github.com/buildkite/go-buildkite/v4"
)

func TestProcess(t *testing.T) {
	rawLog, err := os.ReadFile("testdata/bash-example.log")
	if err != nil {
		t.Fatalf("failed to read test log file: %v", err)
	}

	jobLog := buildkite.JobLog{Content: string(rawLog)}

	// Process the job log
	processedLog, err := Process(jobLog)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// write out golden file if WRITE_JOB_LOG_GOLDEN_FILE is set
	if os.Getenv("WRITE_JOB_LOG_GOLDEN_FILE") != "" {
		err = os.WriteFile("testdata/processed.log", []byte(processedLog), 0600)
		if err != nil {
			t.Fatalf("failed to write processed log golden file: %v", err)
		}
	}

	expectedLog, err := os.ReadFile("testdata/processed.log")
	if err != nil {
		t.Fatalf("failed to read processed log golden file: %v", err)
	}

	// Check if the processed log is as expected
	if processedLog != string(expectedLog) {
		t.Fatalf("expected %q, got %q", expectedLog, processedLog)
	}
}
