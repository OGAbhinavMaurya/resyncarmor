package sentry

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/OGAbhinavMaurya/resyncarmor/pkg/resp"
)

// TuneConfig configures the backlog autotuner.
type TuneConfig struct {
	RedisAddr     string
	AuthPassword  string
	TargetHorizon time.Duration
	MaxMemoryPct  float64
	DryRun        bool
}

// TuneReport summarizes the autotuning calculations and actions.
type TuneReport struct {
	RedisAddr           string  `json:"redis_addr"`
	PreviousBacklog     int64   `json:"previous_backlog_bytes"`
	NewBacklog          int64   `json:"new_backlog_bytes"`
	WriteVelocity       float64 `json:"write_velocity_bytes_per_sec"`
	NewHorizonSeconds   float64 `json:"new_horizon_seconds"`
	DryRun              bool    `json:"dry_run"`
	Applied             bool    `json:"applied"`
	Summary             string  `json:"summary"`
	Actions             []string `json:"actions"`
}

// Autotune evaluates write velocity, checks memory headroom, and adjusts repl-backlog-size.
func Autotune(cfg TuneConfig) (*TuneReport, error) {
	if cfg.TargetHorizon <= 0 {
		cfg.TargetHorizon = 120 * time.Second
	}
	if cfg.MaxMemoryPct <= 0 {
		cfg.MaxMemoryPct = 10.0 // max 10% of memory
	}

	doc := NewDoctor()
	report, err := doc.Inspect(DoctorConfig{
		RedisAddr:     cfg.RedisAddr,
		AuthPassword:  cfg.AuthPassword,
		SampleWindow:  500 * time.Millisecond,
		TargetHorizon: cfg.TargetHorizon,
	})
	if err != nil {
		return nil, fmt.Errorf("pre-tune doctor inspection failed: %w", err)
	}

	var actions []string
	velocity := report.WriteVelocityBytesPerSec
	if velocity <= 0 {
		velocity = 1024 * 1024 // assume 1 MB/s baseline if idle
		actions = append(actions, "Idle write velocity detected; using 1 MB/s baseline for resilience planning")
	}

	// S_target = velocity * TargetHorizon
	neededBytes := int64(math.Ceil(velocity * cfg.TargetHorizon.Seconds()))
	// Enforce minimum 8 MB
	if neededBytes < 8*1024*1024 {
		neededBytes = 8 * 1024 * 1024
	}
	// Round up to nearest MB
	neededBytes = ((neededBytes + (1024*1024 - 1)) / (1024 * 1024)) * (1024 * 1024)

	client := resp.NewClient(cfg.RedisAddr, 5*time.Second)
	if err := client.Connect(); err != nil {
		return nil, err
	}
	defer client.Close()

	if cfg.AuthPassword != "" {
		_ = client.Auth(cfg.AuthPassword)
	}

	// Memory safety verification
	memInfo, _ := client.Info("memory")
	var maxMem int64
	if memInfo != nil {
		maxMem, _ = strconv.ParseInt(memInfo["maxmemory"], 10, 64)
		if maxMem == 0 {
			maxMem, _ = strconv.ParseInt(memInfo["total_system_memory"], 10, 64)
		}
	}

	if maxMem > 0 {
		maxAllowed := int64(float64(maxMem) * (cfg.MaxMemoryPct / 100.0))
		if neededBytes > maxAllowed {
			actions = append(actions, fmt.Sprintf("Capping backlog from %.2f MB to %.2f MB to respect %.0f%% memory ceiling",
				float64(neededBytes)/(1024*1024), float64(maxAllowed)/(1024*1024), cfg.MaxMemoryPct))
			neededBytes = maxAllowed
		}
	}

	applied := false
	if cfg.DryRun {
		actions = append(actions, fmt.Sprintf("[DRY-RUN] Would execute CONFIG SET repl-backlog-size %d (%.2f MB)",
			neededBytes, float64(neededBytes)/(1024*1024)))
	} else {
		if err := client.ConfigSet("repl-backlog-size", strconv.FormatInt(neededBytes, 10)); err != nil {
			return nil, fmt.Errorf("failed to apply CONFIG SET: %w", err)
		}
		applied = true
		actions = append(actions, fmt.Sprintf("Applied CONFIG SET repl-backlog-size %d (%.2f MB)",
			neededBytes, float64(neededBytes)/(1024*1024)))
	}

	newHorizon := float64(neededBytes) / velocity

	summary := fmt.Sprintf("Autotune complete. Backlog scaled from %.2f MB to %.2f MB, extending Replication Safety Horizon to %.1f seconds.",
		float64(report.CurrentBacklogSizeBytes)/(1024*1024), float64(neededBytes)/(1024*1024), newHorizon)

	return &TuneReport{
		RedisAddr:         cfg.RedisAddr,
		PreviousBacklog:   report.CurrentBacklogSizeBytes,
		NewBacklog:        neededBytes,
		WriteVelocity:     velocity,
		NewHorizonSeconds: newHorizon,
		DryRun:            cfg.DryRun,
		Applied:           applied,
		Summary:           summary,
		Actions:           actions,
	}, nil
}
