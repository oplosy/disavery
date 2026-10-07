package lab

import (
	"context"
	"net/http"
	"path/filepath"
	"sync"
	"time"
)

// Env describes how the CLI reaches the lab. Default matches the M1 address
// plan for a CLI running inside the toolbox.
type Env struct {
	Name          string // environment label, "drill"
	Root          string // repository root
	SecretsFile   string
	AgeKeyFile    string
	EscrowFile    string
	JournalPath   string // canary journal
	DNSServer     string
	PublicURL     string
	VaultEndpoint string
	TerraformDir  string
	InventoryPath string
	SSH           SSH
	Now           func() time.Time

	mu       sync.Mutex
	secrets  Secrets
	resolver *Resolver
}

// Default returns the toolbox configuration for a repository at root.
func Default(root string) *Env {
	return &Env{
		Name:          "drill",
		Root:          root,
		SecretsFile:   "/secrets/local.sops.yaml",
		AgeKeyFile:    "/secrets/age/keys.txt",
		EscrowFile:    "/escrow/age-keys.txt",
		JournalPath:   "/state/canary/journal.jsonl",
		DNSServer:     "172.31.0.10:53",
		PublicURL:     "https://docs.disavery.test",
		VaultEndpoint: "vault:9000",
		TerraformDir:  filepath.Join(root, "infra", "terraform", "envs", "local"),
		InventoryPath: filepath.Join(root, "infra", "ansible", "inventory", "hosts.yml"),
		SSH:           SSH{KeyFile: "/secrets/ssh/id_ed25519", User: "root"},
		Now:           time.Now,
	}
}

// Secrets decrypts the secrets file once and caches the result.
func (e *Env) Secrets(ctx context.Context) (Secrets, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.secrets != nil {
		return e.secrets, nil
	}
	s, err := LoadSecrets(ctx, e.SecretsFile)
	if err != nil {
		return nil, err
	}
	e.secrets = s
	return s, nil
}

// Resolver returns the shared TTL-honouring resolver for the lab DNS.
func (e *Env) Resolver() *Resolver {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.resolver == nil {
		e.resolver = &Resolver{Server: e.DNSServer, Timeout: 2 * time.Second, Now: e.Now}
	}
	return e.resolver
}

// HTTPClient returns a client for lab HTTPS endpoints (see NewHTTPClient).
func (e *Env) HTTPClient(ctx context.Context, timeout time.Duration) (*http.Client, error) {
	s, err := e.Secrets(ctx)
	if err != nil {
		return nil, err
	}
	ca, err := s.String("tls", "ca_crt")
	if err != nil {
		return nil, err
	}
	return NewHTTPClient(e.Resolver(), []byte(ca), timeout)
}

// Inventory loads the generated Ansible inventory.
func (e *Env) Inventory() (*Inventory, error) { return LoadInventory(e.InventoryPath) }
