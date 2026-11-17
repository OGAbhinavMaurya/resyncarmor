package main

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"

	"github.com/OGAbhinavMaurya/resyncarmor/pkg/resp"
)

func TestMain_Usage(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printUsage()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("resyncarmor - Redis & Valkey replication backlog sentry")) {
		t.Fatalf("unexpected usage output: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("doctor")) || !bytes.Contains([]byte(output), []byte("tune")) {
		t.Fatalf("expected doctor and tune commands in usage: %s", output)
	}
}

func TestRunDoctor_AgainstMock(t *testing.T) {
	ms, err := resp.NewMockServer()
	if err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}
	defer ms.Close()

	// Update offset in background
	go func() {
		time.Sleep(100 * time.Millisecond)
		ms.SetOffset(2048576)
	}()

	args := []string{
		"--redis", ms.Addr(),
		"--sample", "200ms",
		"--target-horizon", "30s",
		"--json",
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runDoctor(args)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDoctor failed: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("\"redis_addr\":")) {
		t.Fatalf("expected json report containing redis_addr, got: %s", output)
	}
}

func TestRunDoctor_HumanFormat(t *testing.T) {
	ms, err := resp.NewMockServer()
	if err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}
	defer ms.Close()

	args := []string{
		"--redis", ms.Addr(),
		"--sample", "100ms",
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runDoctor(args)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDoctor human output failed: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("RESYNCARMOR DOCTOR REPORT")) {
		t.Fatalf("expected human report header, got: %s", output)
	}
}

func TestRunTune_AgainstMock(t *testing.T) {
	ms, err := resp.NewMockServer()
	if err != nil {
		t.Fatalf("failed to start mock server: %v", err)
	}
	defer ms.Close()

	// 1. Dry run
	argsDry := []string{
		"--redis", ms.Addr(),
		"--target-horizon", "60s",
		"--dry-run",
		"--json",
	}

	err = runTune(argsDry)
	if err != nil {
		t.Fatalf("runTune dry-run failed: %v", err)
	}

	// 2. Live run
	argsLive := []string{
		"--redis", ms.Addr(),
		"--target-horizon", "60s",
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runTune(argsLive)

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runTune live failed: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !bytes.Contains([]byte(output), []byte("RESYNCARMOR TUNE REPORT")) {
		t.Fatalf("expected tune report header, got: %s", output)
	}
}

func TestRunDoctor_InvalidFlag(t *testing.T) {
	err := runDoctor([]string{"--invalid-flag-123"})
	if err == nil {
		t.Fatalf("expected error on invalid flag, got nil")
	}
}

func TestRunTune_InvalidFlag(t *testing.T) {
	err := runTune([]string{"--invalid-flag-123"})
	if err == nil {
		t.Fatalf("expected error on invalid flag, got nil")
	}
}
