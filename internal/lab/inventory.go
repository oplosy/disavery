package lab

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// Inventory is the part of the Terraform-generated Ansible inventory
// (infra/ansible/inventory/hosts.yml) the CLI needs.
type Inventory struct {
	ActiveSite  string
	StandbySite string // "" when there is no warm standby
	Groups      map[string][]string
	Hosts       map[string]map[string]any
}

// LoadInventory parses the generated inventory.
func LoadInventory(path string) (*Inventory, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		All struct {
			Vars     map[string]any `yaml:"vars"`
			Children map[string]struct {
				Hosts map[string]map[string]any `yaml:"hosts"`
			} `yaml:"children"`
		} `yaml:"all"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	inv := &Inventory{Groups: map[string][]string{}, Hosts: map[string]map[string]any{}}
	inv.ActiveSite, _ = raw.All.Vars["active_site"].(string)
	inv.StandbySite, _ = raw.All.Vars["standby_site"].(string)
	if inv.ActiveSite == "" {
		return nil, fmt.Errorf("%s: all.vars.active_site is missing", path)
	}
	for group, g := range raw.All.Children {
		for host, vars := range g.Hosts {
			inv.Groups[group] = append(inv.Groups[group], host)
			if inv.Hosts[host] == nil {
				inv.Hosts[host] = map[string]any{}
			}
			for k, v := range vars {
				inv.Hosts[host][k] = v
			}
		}
		slices.Sort(inv.Groups[group])
	}
	return inv, nil
}

// HostNames returns every host in sorted order.
func (i *Inventory) HostNames() []string {
	names := make([]string, 0, len(i.Hosts))
	for h := range i.Hosts {
		names = append(names, h)
	}
	slices.Sort(names)
	return names
}

// DBHost is the database node of the active site.
func (i *Inventory) DBHost() string { return "db-" + i.ActiveSite }
