package yamltime_test

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/oplosy/disavery/internal/yamltime"
)

func TestUnmarshal(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"d: 30s", 30 * time.Second, false},
		{"d: 1m30s", 90 * time.Second, false},
		{"d: 0s", 0, false},
		{"d: -5s", 0, true},
		{"d: 30", 0, true},
		{"d: [1s]", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			var v struct {
				D yamltime.Duration `yaml:"d"`
			}
			err := yaml.Unmarshal([]byte(tc.in), &v)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && v.D.D() != tc.want {
				t.Fatalf("got %s, want %s", v.D, tc.want)
			}
		})
	}
}
