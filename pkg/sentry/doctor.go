package sentry

import (
	"fmt"
	"strconv"
	"time"

	"github.com/OGAbhinavMaurya/resyncarmor/pkg/kernel"
	"github.com/OGAbhinavMaurya/resyncarmor/pkg/resp"
)

// DoctorConfig configures the replication sentry inspection.
type DoctorConfig struct {
	RedisAddr     string
	AuthPassword  string
	SampleWindow  time.Duration
	TargetHorizon time.Duration
	ProcRoot      string
	THPPath       string
}

// DoctorReport contains replication health, write velocity, and safety horizon findings.
type DoctorReport struct {
	RedisAddr               string            `json:"redis_addr"`
	Role                    string            `json:"role"`
	ConnectedReplicas       int               `json:"connected_replicas"`
	CurrentBacklogSizeBytes int64             `json:"current_backlog_size_bytes"`
	WriteVelocityBytesPerSec float64          `json:"write_velocity_bytes_per_sec"`
	SafetyHorizonSeconds    float64           `json:"safety_horizon_seconds"`
	TargetHorizonSeconds    float64           `json:"target_horizon_seconds"`
	THPMode                 string            `json:"thp_mode"`
	THPHazardous            bool              `json:"thp_hazardous"`
	PrivateDirtyBytes       int64             `json:"private_dirty_bytes"`
	Status                  string            `json:"status"` // HEALTHY, WARNING, CRITICAL
	Summary                 string            `json:"summary"`
	Recommendation          string            `json:"recommendation"`
	DiagnosticLog           []string          `json:"diagnostic_log"`
}

// Doctor diagnoses replication death spiral risks in Redis and Valkey instances.
type Doctor struct{}

// NewDoctor creates a new Doctor sentry.
func NewDoctor() *Doctor {
	return &Doctor{}
}

// Inspect evaluates write velocity and calculates the Replication Safety Horizon.
func (d *Doctor) Inspect(cfg DoctorConfig) (*DoctorReport, error) {
	if cfg.SampleWindow <= 0 {
		cfg.SampleWindow = 500 * time.Millisecond
	}
	if cfg.TargetHorizon <= 0 {
		cfg.TargetHorizon = 60 * time.Second
	}

	client := resp.NewClient(cfg.RedisAddr, 5*time.Second)
	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis at %s: %w", cfg.RedisAddr, err)
	}
	defer client.Close()

	if cfg.AuthPassword != "" {
		if err := client.Auth(cfg.AuthPassword); err != nil {
			return nil, fmt.Errorf("auth failed: %w", err)
		}
	}

	var logs []string

	// Sample 1: replication info
	info1, err := client.Info("replication")
	if err != nil {
		return nil, fmt.Errorf("read info replication: %w", err)
	}
	t1 := time.Now()
	offset1, _ := strconv.ParseInt(info1["master_repl_offset"], 10, 64)
	backlogSize, _ := strconv.ParseInt(info1["repl_backlog_size"], 10, 64)
	connectedSlaves, _ := strconv.Atoi(info1["connected_slaves"])
	role := info1["role"]

	// Sleep sample window to measure write velocity
	time.Sleep(cfg.SampleWindow)

	// Sample 2: replication info
	info2, err := client.Info("replication")
	if err != nil {
		return nil, fmt.Errorf("read info replication sample 2: %w", err)
	}
	t2 := time.Now()
	offset2, _ := strconv.ParseInt(info2["master_repl_offset"], 10, 64)

	deltaBytes := offset2 - offset1
	durationSec := t2.Sub(t1).Seconds()
	var writeVelocity float64
	if durationSec > 0 && deltaBytes >= 0 {
		writeVelocity = float64(deltaBytes) / durationSec
	}

	// Compute Replication Safety Horizon (T_horizon = backlog / velocity)
	var horizonSec float64 = 999999
	if writeVelocity > 0 && backlogSize > 0 {
		horizonSec = float64(backlogSize) / writeVelocity
	}

	logs = append(logs, fmt.Sprintf("Write velocity: %.2f KB/s (delta: %d bytes in %.2fs)",
		writeVelocity/1024.0, deltaBytes, durationSec))
	logs = append(logs, fmt.Sprintf("Current backlog: %d bytes (%.2f MB), Safety Horizon: %.2f seconds",
		backlogSize, float64(backlogSize)/(1024*1024), horizonSec))

	// Check kernel THP
	thpStatus, _ := kernel.CheckTHP(cfg.THPPath)
	thpMode := "unknown"
	thpHazard := false
	if thpStatus != nil {
		thpMode = thpStatus.ActiveMode
		thpHazard = thpStatus.IsHazardous
		if thpHazard {
			logs = append(logs, "Kernel Warning: Transparent Huge Pages is [always]; 2MB page allocation on BGSAVE Copy-on-Write")
		}
	}

	// Read smaps for Redis PID if available
	serverInfo, _ := client.Info("server")
	var privateDirty int64
	if serverInfo != nil {
		if pidStr, ok := serverInfo["process_id"]; ok {
			if pid, err := strconv.Atoi(pidStr); err == nil {
				if smaps, err := kernel.ReadSmapsRollup(pid, cfg.ProcRoot); err == nil {
					privateDirty = smaps.PrivateDirty
					logs = append(logs, fmt.Sprintf("Process %d Copy-on-Write memory (Private_Dirty): %.2f MB",
						pid, float64(privateDirty)/(1024*1024)))
				}
			}
		}
	}

	status := "HEALTHY"
	summary := fmt.Sprintf("Replication backlog is healthy. Safety horizon (%.1fs) exceeds target horizon (%.0fs).",
		horizonSec, cfg.TargetHorizon.Seconds())
	recommendation := "Replication buffer parameters are adequate for current write throughput."

	if horizonSec < 10.0 {
		status = "CRITICAL"
		summary = fmt.Sprintf("Critical replication death spiral risk. Safety horizon is only %.2fs (backlog: %.2f MB, writes: %.2f KB/s). Transient network jitter will force catastrophic +FULLRESYNC.",
			horizonSec, float64(backlogSize)/(1024*1024), writeVelocity/1024.0)
		recommendation = fmt.Sprintf("Run 'resyncarmor tune' immediately to expand repl-backlog-size to match write velocity.")
	} else if horizonSec < cfg.TargetHorizon.Seconds() || thpHazard {
		status = "WARNING"
		summary = fmt.Sprintf("Narrow replication safety margin. Horizon is %.1fs (target: %.0fs).", horizonSec, cfg.TargetHorizon.Seconds())
		recommendation = "Consider scaling repl-backlog-size and disabling Transparent Huge Pages."
	}

	return &DoctorReport{
		RedisAddr:                cfg.RedisAddr,
		Role:                     role,
		ConnectedReplicas:        connectedSlaves,
		CurrentBacklogSizeBytes:  backlogSize,
		WriteVelocityBytesPerSec: writeVelocity,
		SafetyHorizonSeconds:     horizonSec,
		TargetHorizonSeconds:     cfg.TargetHorizon.Seconds(),
		THPMode:                  thpMode,
		THPHazardous:             thpHazard,
		PrivateDirtyBytes:        privateDirty,
		Status:                   status,
		Summary:                  summary,
		Recommendation:           recommendation,
		DiagnosticLog:            logs,
	}, nil
}
