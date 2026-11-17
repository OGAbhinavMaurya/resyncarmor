package sentry

import (
	"testing"
	"time"

	"github.com/OGAbhinavMaurya/resyncarmor/pkg/resp"
)

func TestDoctor_Inspect(t *testing.T) {
	ms, err := resp.NewMockServer()
	if err != nil {
		t.Fatalf("NewMockServer failed: %v", err)
	}
	defer ms.Close()

	doc := NewDoctor()
	rep, err := doc.Inspect(DoctorConfig{
		RedisAddr:     ms.Addr(),
		SampleWindow:  100 * time.Millisecond,
		TargetHorizon: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}

	if rep.Role != "master" {
		t.Errorf("expected role master, got %s", rep.Role)
	}
	if rep.CurrentBacklogSizeBytes != 1048576 {
		t.Errorf("expected 1MB backlog, got %d", rep.CurrentBacklogSizeBytes)
	}
}

func TestAutotune_DryRunAndLive(t *testing.T) {
	ms, err := resp.NewMockServer()
	if err != nil {
		t.Fatalf("NewMockServer failed: %v", err)
	}
	defer ms.Close()

	// Dry run
	tuneRep, err := Autotune(TuneConfig{
		RedisAddr:     ms.Addr(),
		TargetHorizon: 60 * time.Second,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("Autotune dry run failed: %v", err)
	}
	if !tuneRep.DryRun || tuneRep.Applied {
		t.Errorf("expected DryRun=true, Applied=false")
	}

	// Live run
	liveRep, err := Autotune(TuneConfig{
		RedisAddr:     ms.Addr(),
		TargetHorizon: 60 * time.Second,
		DryRun:        false,
	})
	if err != nil {
		t.Fatalf("Autotune live run failed: %v", err)
	}
	if liveRep.DryRun || !liveRep.Applied {
		t.Errorf("expected DryRun=false, Applied=true")
	}

	// Verify mock server config was updated
	newCfg := ms.GetConfig("repl-backlog-size")
	if newCfg == "1048576" {
		t.Errorf("expected repl-backlog-size to be expanded from 1MB, got %s", newCfg)
	}
}
