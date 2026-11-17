package kernel

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SmapsSummary captures memory allocation and Copy-on-Write metrics from /proc/<pid>/smaps_rollup.
type SmapsSummary struct {
	PID          int   `json:"pid"`
	RssBytes     int64 `json:"rss_bytes"`
	PssBytes     int64 `json:"pss_bytes"`
	PrivateDirty int64 `json:"private_dirty_bytes"` // Copy-on-Write memory allocated
	SharedDirty  int64 `json:"shared_dirty_bytes"`
	Anonymous    int64 `json:"anonymous_bytes"`
}

// ReadSmapsRollup parses /proc/<pid>/smaps_rollup or returns error.
func ReadSmapsRollup(pid int, procRoot string) (*SmapsSummary, error) {
	if procRoot == "" {
		procRoot = "/proc"
	}

	path := filepath.Join(procRoot, strconv.Itoa(pid), "smaps_rollup")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	summary := &SmapsSummary{PID: pid}
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.TrimSuffix(parts[0], ":")
		valKB, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		valBytes := valKB * 1024

		switch key {
		case "Rss":
			summary.RssBytes = valBytes
		case "Pss":
			summary.PssBytes = valBytes
		case "Private_Dirty":
			summary.PrivateDirty = valBytes
		case "Shared_Dirty":
			summary.SharedDirty = valBytes
		case "Anonymous":
			summary.Anonymous = valBytes
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read smaps_rollup: %w", err)
	}

	return summary, nil
}
