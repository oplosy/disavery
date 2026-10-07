package lab_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/lab"
)

func TestTopologyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.auto.tfvars")
	got, err := lab.ReadTopology(path)
	if err != nil || got != lab.DefaultTopology {
		t.Fatalf("missing file: %+v %v", got, err)
	}
	warm := lab.Topology{ActiveSite: "a", StandbySite: "b", SiteAEnabled: true, SiteBEnabled: true}
	if err := lab.WriteTopology(path, warm); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `standby_site   = "b"`) {
		t.Fatalf("file:\n%s", b)
	}
	if got, err := lab.ReadTopology(path); err != nil || got != warm {
		t.Fatalf("read back %+v %v", got, err)
	}
}

func TestTopologyValidation(t *testing.T) {
	tests := []struct {
		name string
		set  [][2]string
		want string
	}{
		{"standby equals active", [][2]string{{"standby_site", "a"}}, "must differ"},
		{"standby site absent", [][2]string{{"standby_site", "b"}}, "site b is active or standby but not enabled"},
		{"unknown site", [][2]string{{"active_site", "c"}}, "active_site must be a or b"},
		{"active site disabled", [][2]string{{"site_a_enabled", "false"}}, "site a is active or standby but not enabled"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			top := lab.DefaultTopology
			for _, kv := range tc.set {
				if err := top.Set(kv[0], kv[1]); err != nil {
					t.Fatal(err)
				}
			}
			if err := top.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	top := lab.DefaultTopology
	for _, kv := range [][2]string{{"region", "eu"}, {"site_b_enabled", "maybe"}} {
		if err := top.Set(kv[0], kv[1]); err == nil {
			t.Fatalf("Set(%v): want error", kv)
		}
	}
}
