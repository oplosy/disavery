// Package config loads docsvc settings from environment variables.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
)

// Config holds docsvc runtime settings.
type Config struct {
	Listen            string
	DatabaseURL       string
	S3Endpoint        string
	S3AccessKey       string
	S3SecretKey       string
	S3Bucket          string
	S3UseTLS          bool
	PaymentWebhookURL string
	PublicBaseURL     string
	WebhookSecret     string
	MaxUploadBytes    int64
}

// FromEnv builds a Config using getenv (normally os.Getenv). It reports every
// problem at once so a misconfigured node shows all errors in one log line.
func FromEnv(getenv func(string) string) (Config, error) {
	c := Config{
		Listen:            cmp.Or(getenv("DOCSVC_LISTEN"), ":8080"),
		DatabaseURL:       getenv("DOCSVC_DATABASE_URL"),
		S3Endpoint:        getenv("DOCSVC_S3_ENDPOINT"),
		S3AccessKey:       getenv("DOCSVC_S3_ACCESS_KEY"),
		S3SecretKey:       getenv("DOCSVC_S3_SECRET_KEY"),
		S3Bucket:          cmp.Or(getenv("DOCSVC_S3_BUCKET"), "attachments"),
		PaymentWebhookURL: getenv("DOCSVC_PAYMENT_WEBHOOK_URL"),
		PublicBaseURL:     getenv("DOCSVC_PUBLIC_BASE_URL"),
		WebhookSecret:     getenv("DOCSVC_WEBHOOK_SECRET"),
		MaxUploadBytes:    10 << 20,
	}

	var errs []error
	required := []struct{ name, value string }{
		{"DOCSVC_DATABASE_URL", c.DatabaseURL},
		{"DOCSVC_S3_ENDPOINT", c.S3Endpoint},
		{"DOCSVC_S3_ACCESS_KEY", c.S3AccessKey},
		{"DOCSVC_S3_SECRET_KEY", c.S3SecretKey},
		{"DOCSVC_WEBHOOK_SECRET", c.WebhookSecret},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", r.name))
		}
	}
	if c.PaymentWebhookURL != "" && c.PublicBaseURL == "" {
		errs = append(errs, errors.New("DOCSVC_PUBLIC_BASE_URL is required when DOCSVC_PAYMENT_WEBHOOK_URL is set"))
	}
	if v := getenv("DOCSVC_S3_USE_TLS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("DOCSVC_S3_USE_TLS: %w", err))
		}
		c.S3UseTLS = b
	}
	if v := getenv("DOCSVC_MAX_UPLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("DOCSVC_MAX_UPLOAD_BYTES must be a positive integer, got %q", v))
		}
		c.MaxUploadBytes = n
	}
	return c, errors.Join(errs...)
}
