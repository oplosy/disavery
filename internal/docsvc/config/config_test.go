package config_test

import (
	"strings"
	"testing"

	"github.com/oplosy/disavery/internal/docsvc/config"
)

func validEnv() map[string]string {
	return map[string]string{
		"DOCSVC_DATABASE_URL":   "postgres://docsvc@db-a/docsvc",
		"DOCSVC_S3_ENDPOINT":    "obj-a:9000",
		"DOCSVC_S3_ACCESS_KEY":  "docsvc",
		"DOCSVC_S3_SECRET_KEY":  "secret",
		"DOCSVC_WEBHOOK_SECRET": "hmac",
	}
}

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvDefaults(t *testing.T) {
	c, err := config.FromEnv(getenv(validEnv()))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Listen != ":8080" || c.S3Bucket != "attachments" || c.MaxUploadBytes != 10<<20 || c.S3UseTLS {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	env := validEnv()
	env["DOCSVC_LISTEN"] = ":9090"
	env["DOCSVC_S3_USE_TLS"] = "true"
	env["DOCSVC_MAX_UPLOAD_BYTES"] = "2048"
	c, err := config.FromEnv(getenv(env))
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if c.Listen != ":9090" || !c.S3UseTLS || c.MaxUploadBytes != 2048 {
		t.Fatalf("overrides not applied: %+v", c)
	}
}

func TestFromEnvErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string)
		want   string
	}{
		{"missing database url", func(m map[string]string) { delete(m, "DOCSVC_DATABASE_URL") }, "DOCSVC_DATABASE_URL is required"},
		{"missing webhook secret", func(m map[string]string) { delete(m, "DOCSVC_WEBHOOK_SECRET") }, "DOCSVC_WEBHOOK_SECRET is required"},
		{"webhook url without public url", func(m map[string]string) { m["DOCSVC_PAYMENT_WEBHOOK_URL"] = "http://webhook:8081/payments" }, "DOCSVC_PUBLIC_BASE_URL is required"},
		{"bad tls flag", func(m map[string]string) { m["DOCSVC_S3_USE_TLS"] = "maybe" }, "DOCSVC_S3_USE_TLS"},
		{"zero upload limit", func(m map[string]string) { m["DOCSVC_MAX_UPLOAD_BYTES"] = "0" }, "DOCSVC_MAX_UPLOAD_BYTES must be a positive integer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			tc.mutate(env)
			_, err := config.FromEnv(getenv(env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
