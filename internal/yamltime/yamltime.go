// Package yamltime reads Go duration strings ("30s", "15m") from YAML.
package yamltime

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration written as a Go duration string in YAML.
type Duration time.Duration

// UnmarshalYAML parses a non-negative duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: duration must be a string such as \"30s\"", n.Line)
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	if v < 0 {
		return fmt.Errorf("line %d: duration %q must not be negative", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }
