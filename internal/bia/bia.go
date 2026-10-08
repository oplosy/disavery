// Package bia loads the business impact analysis: recovery targets per DR tier
// and per scenario, and the thresholds drills are judged against (docs/bia.yaml).
package bia

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/oplosy/disavery/internal/yamltime"
)

// Targets are the recovery objectives of one DR tier.
type Targets struct {
	RPO time.Duration
	RTO time.Duration
}

// Tier is one DR tier as declared in bia.yaml.
type Tier struct {
	RPO       yamltime.Duration `yaml:"rpo"`
	RTO       yamltime.Duration `yaml:"rto"`
	Mechanism string            `yaml:"mechanism"`
	// Cost is the proxy of spec success criterion 5: what runs all the time
	// for this tier, and an estimated monthly cloud price (see docs/bia.md).
	Cost struct {
		AlwaysOnNodes int     `yaml:"always_on_nodes"`
		MonthlyUSD    float64 `yaml:"monthly_usd"`
	} `yaml:"cost"`
}

// Scenario holds the targets of a scenario that does not depend on the DR
// tier: an incident repaired inside the production site (S2–S5).
type Scenario struct {
	RPO yamltime.Duration `yaml:"rpo"`
	RTO yamltime.Duration `yaml:"rto"`
}

// BIA is the parsed bia.yaml.
type BIA struct {
	Service        string              `yaml:"service"`
	Classification string              `yaml:"classification"`
	Tiers          map[string]Tier     `yaml:"tiers"`
	Scenarios      map[string]Scenario `yaml:"scenarios"`
	RestoreTest    struct {
		MaxDuration   yamltime.Duration `yaml:"max_duration"`
		PITRTolerance yamltime.Duration `yaml:"pitr_tolerance"`
	} `yaml:"restore_test"`
	Preflight struct {
		MaxBackupAge     yamltime.Duration `yaml:"max_backup_age"`
		MaxArchiveAge    yamltime.Duration `yaml:"max_archive_age"`
		MaxCanarySilence yamltime.Duration `yaml:"max_canary_silence"`
		// MinCanaryHistory is how long the canary must have written without a
		// gap, all of it held by production, before an incident: a drill
		// measures one incident, not the tail of an earlier one.
		MinCanaryHistory yamltime.Duration `yaml:"min_canary_history"`
	} `yaml:"preflight"`
}

// Load reads and validates a bia.yaml file. Unknown keys are errors.
func Load(path string) (*BIA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var b BIA
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := b.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

func (b *BIA) validate() error {
	var errs []error
	if len(b.Tiers) == 0 {
		errs = append(errs, errors.New("tiers: at least one tier is required"))
	}
	for name, t := range b.Tiers {
		if t.RPO <= 0 || t.RTO <= 0 {
			errs = append(errs, fmt.Errorf("tiers.%s: rpo and rto must be positive", name))
		}
	}
	for name, sc := range b.Scenarios {
		if sc.RPO <= 0 || sc.RTO <= 0 {
			errs = append(errs, fmt.Errorf("scenarios.%s: rpo and rto must be positive", name))
		}
	}
	positive := []struct {
		name string
		v    yamltime.Duration
	}{
		{"restore_test.max_duration", b.RestoreTest.MaxDuration},
		{"preflight.max_backup_age", b.Preflight.MaxBackupAge},
		{"preflight.max_archive_age", b.Preflight.MaxArchiveAge},
		{"preflight.max_canary_silence", b.Preflight.MaxCanarySilence},
		{"preflight.min_canary_history", b.Preflight.MinCanaryHistory},
	}
	for _, p := range positive {
		if p.v <= 0 {
			errs = append(errs, fmt.Errorf("%s must be positive", p.name))
		}
	}
	return errors.Join(errs...)
}

// TierNames returns the declared tiers in sorted order.
func (b *BIA) TierNames() []string {
	names := make([]string, 0, len(b.Tiers))
	for n := range b.Tiers {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// Targets returns the objectives of a tier.
func (b *BIA) Targets(tier string) (Targets, error) {
	t, ok := b.Tiers[tier]
	if !ok {
		return Targets{}, fmt.Errorf("unknown tier %q", tier)
	}
	return Targets{RPO: t.RPO.D(), RTO: t.RTO.D()}, nil
}

// ScenarioTargets returns the objectives of a tierless scenario, if declared.
func (b *BIA) ScenarioTargets(id string) (Targets, bool) {
	sc, ok := b.Scenarios[id]
	if !ok {
		return Targets{}, false
	}
	return Targets{RPO: sc.RPO.D(), RTO: sc.RTO.D()}, true
}
