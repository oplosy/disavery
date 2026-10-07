package bia_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oplosy/disavery/internal/bia"
)

func TestLoadRepositoryFile(t *testing.T) {
	b, err := bia.Load("../../docs/bia.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !slices.Equal(b.TierNames(), []string{"pilot-light", "warm-standby"}) {
		t.Fatalf("tiers %v", b.TierNames())
	}
	warm, err := b.Targets("warm-standby")
	if err != nil || warm.RPO != 5*time.Second || warm.RTO != 2*time.Minute {
		t.Fatalf("warm-standby targets %+v, %v", warm, err)
	}
	if _, err := b.Targets("cold"); err == nil {
		t.Fatal("want error for unknown tier")
	}
}

const valid = `
tiers:
  t1: {rpo: 1s, rto: 1m}
restore_test: {max_duration: 10m, pitr_tolerance: 1s}
preflight: {max_backup_age: 1h, max_archive_age: 1m, max_canary_silence: 5s}
`

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"unknown key", valid + "extra: 1\n", "field extra not found"},
		{"no tiers", strings.Replace(valid, "t1: {rpo: 1s, rto: 1m}", "{}", 1), "at least one tier"},
		{"zero rto", strings.Replace(valid, "rto: 1m", "rto: 0s", 1), "tiers.t1: rpo and rto must be positive"},
		{"missing restore test", strings.Replace(valid, "max_duration: 10m, ", "", 1), "restore_test.max_duration must be positive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bia.yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := bia.Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
