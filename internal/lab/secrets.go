package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Secrets is the decrypted SOPS document.
type Secrets map[string]any

// LoadSecrets decrypts file with the sops CLI (SOPS_AGE_KEY_FILE must be set).
func LoadSecrets(ctx context.Context, file string) (Secrets, error) {
	out, err := exec.CommandContext(ctx, "sops", "-d", "--output-type", "json", file).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("decrypt %s: %w: %s", file, err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("decrypt %s: %w", file, err)
	}
	var s Secrets
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", file, err)
	}
	return s, nil
}

// String returns the string at path, e.g. String("minio", "vault_root_user").
func (s Secrets) String(path ...string) (string, error) {
	var cur any = map[string]any(s)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("secret %s: not found", strings.Join(path, "."))
		}
		cur = m[p]
	}
	v, ok := cur.(string)
	if !ok {
		return "", fmt.Errorf("secret %s: not found", strings.Join(path, "."))
	}
	return v, nil
}
