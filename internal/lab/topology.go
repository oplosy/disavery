package lab

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// Topology is which site serves and which (if any) runs a warm standby. It
// lives in the Terraform environment as topology.auto.tfvars, which Terraform
// loads automatically, so `make up` never undoes a failover.
type Topology struct {
	ActiveSite   string // "a" or "b"
	StandbySite  string // "", "a" or "b"
	SiteAEnabled bool
	SiteBEnabled bool
}

// DefaultTopology is the pilot-light baseline: site-a serves, site-b absent.
var DefaultTopology = Topology{ActiveSite: "a", SiteAEnabled: true}

// Validate checks the combination is meaningful.
func (t Topology) Validate() error {
	var errs []error
	if t.ActiveSite != "a" && t.ActiveSite != "b" {
		errs = append(errs, fmt.Errorf("active_site must be a or b, not %q", t.ActiveSite))
	}
	if t.StandbySite != "" && t.StandbySite != "a" && t.StandbySite != "b" {
		errs = append(errs, fmt.Errorf("standby_site must be empty, a or b, not %q", t.StandbySite))
	}
	if t.StandbySite != "" && t.StandbySite == t.ActiveSite {
		errs = append(errs, errors.New("standby_site must differ from active_site"))
	}
	for _, s := range []string{t.ActiveSite, t.StandbySite} {
		if s != "" && !t.Enabled(s) {
			errs = append(errs, fmt.Errorf("site %s is active or standby but not enabled", s))
		}
	}
	return errors.Join(errs...)
}

// Enabled reports whether a site's nodes exist.
func (t Topology) Enabled(site string) bool {
	return (site == "a" && t.SiteAEnabled) || (site == "b" && t.SiteBEnabled)
}

// Set changes one variable, e.g. Set("active_site", "b").
func (t *Topology) Set(key, value string) error {
	switch key {
	case "active_site":
		t.ActiveSite = value
	case "standby_site":
		t.StandbySite = value
	case "site_a_enabled", "site_b_enabled":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if key == "site_a_enabled" {
			t.SiteAEnabled = b
		} else {
			t.SiteBEnabled = b
		}
	default:
		return fmt.Errorf("unknown topology variable %q (active_site, standby_site, site_a_enabled, site_b_enabled)", key)
	}
	return nil
}

// ReadTopology reads the tfvars file; a missing file is DefaultTopology.
func ReadTopology(path string) (Topology, error) {
	t := DefaultTopology
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return t, fmt.Errorf("%s:%d: expected key = value", path, n)
		}
		if err := t.Set(strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"`)); err != nil {
			return t, fmt.Errorf("%s:%d: %w", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return t, err
	}
	return t, t.Validate()
}

// WriteTopology validates t and writes it as tfvars.
func WriteTopology(path string, t Topology) error {
	if err := t.Validate(); err != nil {
		return err
	}
	content := fmt.Sprintf(`# Managed by `+"`disavery env set`"+`: the lab's current topology.
active_site    = %q
standby_site   = %q
site_a_enabled = %t
site_b_enabled = %t
`, t.ActiveSite, t.StandbySite, t.SiteAEnabled, t.SiteBEnabled)
	return os.WriteFile(path, []byte(content), 0o644)
}
