package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/OGAbhinavMaurya/resyncarmor/pkg/sentry"
)

const version = "1.0.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	var err error
	switch command {
	case "doctor":
		err = runDoctor(args)
	case "tune":
		err = runTune(args)
	case "version", "-v", "--version":
		fmt.Printf("resyncarmor version %s\n", version)
		return
	case "help", "-h", "--help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	usage := `resyncarmor - Redis & Valkey replication backlog sentry and COW memory guard

Usage:
  resyncarmor <command> [options]

Commands:
  doctor     Inspect replication backlog, measure write velocity, and calculate safety horizon
  tune       Autotune repl-backlog-size dynamically based on write throughput and RAM headroom
  version    Show resyncarmor version
  help       Show this help message

Doctor Options:
  --redis            Target Redis or Valkey server address [default: 127.0.0.1:6379]
  --auth             Redis authentication password
  --sample           Write velocity sampling window duration [default: 1s]
  --target-horizon   Target replication safety horizon duration [default: 60s]
  --proc-root        Path to Linux /proc filesystem [default: /proc]
  --thp-path         Path to Transparent Huge Pages sysfs node
  --json             Output results in JSON format

Tune Options:
  --redis            Target Redis or Valkey server address [default: 127.0.0.1:6379]
  --auth             Redis authentication password
  --target-horizon   Target replication safety horizon duration [default: 120s]
  --max-memory-pct   Maximum percentage of available memory to allocate [default: 10.0]
  --dry-run          Preview autotune calculations without modifying Redis configuration
  --json             Output results in JSON format

Examples:
  resyncarmor doctor --redis 127.0.0.1:6379
  resyncarmor doctor --redis 127.0.0.1:6379 --json
  resyncarmor tune --redis 127.0.0.1:6379 --target-horizon 120s --dry-run
  resyncarmor tune --redis 127.0.0.1:6379 --target-horizon 120s
`
	fmt.Print(usage)
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	redisAddr := fs.String("redis", "127.0.0.1:6379", "Redis server address")
	authPassword := fs.String("auth", "", "Redis password")
	sampleWindow := fs.Duration("sample", 1*time.Second, "Sampling duration")
	targetHorizon := fs.Duration("target-horizon", 60*time.Second, "Target safety horizon")
	procRoot := fs.String("proc-root", "/proc", "Path to proc filesystem")
	thpPath := fs.String("thp-path", "/sys/kernel/mm/transparent_hugepage/enabled", "Path to THP sysfs")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	if err := fs.Parse(args); err != nil {
		return err
	}

	doc := sentry.NewDoctor()
	report, err := doc.Inspect(sentry.DoctorConfig{
		RedisAddr:     *redisAddr,
		AuthPassword:  *authPassword,
		SampleWindow:  *sampleWindow,
		TargetHorizon: *targetHorizon,
		ProcRoot:      *procRoot,
		THPPath:       *thpPath,
	})
	if err != nil {
		return fmt.Errorf("doctor inspection failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Println("================================================================================")
	fmt.Println("                           RESYNCARMOR DOCTOR REPORT                            ")
	fmt.Println("================================================================================")
	fmt.Printf(" Redis Address:             %s (Role: %s, Replicas: %d)\n",
		report.RedisAddr, report.Role, report.ConnectedReplicas)
	fmt.Printf(" Current Backlog Size:      %d bytes (%.2f MB)\n",
		report.CurrentBacklogSizeBytes, float64(report.CurrentBacklogSizeBytes)/(1024*1024))
	fmt.Printf(" Measured Write Velocity:   %.2f KB/s (%.2f MB/s)\n",
		report.WriteVelocityBytesPerSec/1024.0, report.WriteVelocityBytesPerSec/(1024*1024))
	fmt.Printf(" Safety Horizon:            %.2f seconds (Target: %.0f seconds)\n",
		report.SafetyHorizonSeconds, report.TargetHorizonSeconds)
	fmt.Printf(" Kernel THP Mode:           [%s] (Hazardous: %v)\n", report.THPMode, report.THPHazardous)
	if report.PrivateDirtyBytes > 0 {
		fmt.Printf(" COW Memory (PrivateDirty): %.2f MB\n", float64(report.PrivateDirtyBytes)/(1024*1024))
	}
	fmt.Printf(" Status:                    [%s]\n", report.Status)
	fmt.Printf(" Summary:                   %s\n", report.Summary)
	fmt.Printf(" Recommendation:            %s\n", report.Recommendation)
	fmt.Println("--------------------------------------------------------------------------------")
	if len(report.DiagnosticLog) > 0 {
		fmt.Println(" Diagnostic Trace:")
		for _, log := range report.DiagnosticLog {
			fmt.Printf("   * %s\n", log)
		}
	}
	fmt.Println("================================================================================")

	return nil
}

func runTune(args []string) error {
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	redisAddr := fs.String("redis", "127.0.0.1:6379", "Redis server address")
	authPassword := fs.String("auth", "", "Redis password")
	targetHorizon := fs.Duration("target-horizon", 120*time.Second, "Target safety horizon")
	maxMemoryPct := fs.Float64("max-memory-pct", 10.0, "Max memory percentage for backlog")
	dryRun := fs.Bool("dry-run", false, "Dry run mode")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	if err := fs.Parse(args); err != nil {
		return err
	}

	report, err := sentry.Autotune(sentry.TuneConfig{
		RedisAddr:     *redisAddr,
		AuthPassword:  *authPassword,
		TargetHorizon: *targetHorizon,
		MaxMemoryPct:  *maxMemoryPct,
		DryRun:        *dryRun,
	})
	if err != nil {
		return fmt.Errorf("autotune failed: %w", err)
	}

	if *jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Println("================================================================================")
	fmt.Println("                           RESYNCARMOR TUNE REPORT                              ")
	fmt.Println("================================================================================")
	fmt.Printf(" Redis Address:             %s\n", report.RedisAddr)
	fmt.Printf(" Mode:                      DryRun=%v (Applied: %v)\n", report.DryRun, report.Applied)
	fmt.Printf(" Previous Backlog Size:     %d bytes (%.2f MB)\n",
		report.PreviousBacklog, float64(report.PreviousBacklog)/(1024*1024))
	fmt.Printf(" Tuned Backlog Size:        %d bytes (%.2f MB)\n",
		report.NewBacklog, float64(report.NewBacklog)/(1024*1024))
	fmt.Printf(" New Safety Horizon:        %.2f seconds\n", report.NewHorizonSeconds)
	fmt.Printf(" Summary:                   %s\n", report.Summary)
	fmt.Println("--------------------------------------------------------------------------------")
	if len(report.Actions) > 0 {
		fmt.Println(" Tuning Actions:")
		for _, a := range report.Actions {
			fmt.Printf("   * %s\n", a)
		}
	}
	fmt.Println("================================================================================")

	return nil
}
