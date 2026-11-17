package kernel

import (
	"os"
	"strings"
)

// THPStatus represents Linux Transparent Huge Pages configuration.
type THPStatus struct {
	RawSetting string `json:"raw_setting"`
	ActiveMode string `json:"active_mode"` // "always", "madvise", "never", "unknown"
	IsHazardous bool  `json:"is_hazardous"` // true if "always"
}

// CheckTHP reads /sys/kernel/mm/transparent_hugepage/enabled or custom path.
func CheckTHP(path string) (*THPStatus, error) {
	if path == "" {
		path = "/sys/kernel/mm/transparent_hugepage/enabled"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return &THPStatus{
			ActiveMode: "unknown",
		}, err
	}

	raw := strings.TrimSpace(string(data))
	var active string

	// Linux formats active option in brackets, e.g. "[always] madvise never"
	for _, part := range strings.Fields(raw) {
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			active = strings.Trim(part, "[]")
			break
		}
	}

	if active == "" {
		active = "unknown"
	}

	isHazard := active == "always"

	return &THPStatus{
		RawSetting:  raw,
		ActiveMode:  active,
		IsHazardous: isHazard,
	}, nil
}
