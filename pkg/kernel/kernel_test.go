package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckTHP(t *testing.T) {
	tempDir := t.TempDir()

	// Test always
	p1 := filepath.Join(tempDir, "thp_always")
	_ = os.WriteFile(p1, []byte("[always] madvise never\n"), 0644)
	s1, err := CheckTHP(p1)
	if err != nil || s1.ActiveMode != "always" || !s1.IsHazardous {
		t.Errorf("expected always hazardous, got %+v, err: %v", s1, err)
	}

	// Test never
	p2 := filepath.Join(tempDir, "thp_never")
	_ = os.WriteFile(p2, []byte("always madvise [never]\n"), 0644)
	s2, err := CheckTHP(p2)
	if err != nil || s2.ActiveMode != "never" || s2.IsHazardous {
		t.Errorf("expected never non-hazardous, got %+v, err: %v", s2, err)
	}
}

func TestReadSmapsRollup(t *testing.T) {
	tempDir := t.TempDir()
	pidDir := filepath.Join(tempDir, "12345")
	_ = os.MkdirAll(pidDir, 0755)

	smapsContent := `10000000-20000000 ---p 00000000 00:00 0 [rollup]
Rss:              1048576 kB
Pss:              1048576 kB
Shared_Clean:           0 kB
Shared_Dirty:           0 kB
Private_Clean:          0 kB
Private_Dirty:     262144 kB
Anonymous:        1048576 kB
`
	_ = os.WriteFile(filepath.Join(pidDir, "smaps_rollup"), []byte(smapsContent), 0644)

	summary, err := ReadSmapsRollup(12345, tempDir)
	if err != nil {
		t.Fatalf("ReadSmapsRollup failed: %v", err)
	}

	if summary.RssBytes != 1048576*1024 {
		t.Errorf("unexpected RssBytes: %d", summary.RssBytes)
	}
	if summary.PrivateDirty != 262144*1024 {
		t.Errorf("unexpected PrivateDirty: %d", summary.PrivateDirty)
	}
}
