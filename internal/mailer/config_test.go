package mailer

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConfigFromEnvDisabledSkipsValidation(t *testing.T) {
	cfg, err := ConfigFromEnv(map[string]string{"SMTP_ENABLED": "false", "SMTP_HOST": "  "})
	if err != nil {
		t.Fatalf("disabled config should not fail validation: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("expected SMTP to stay disabled")
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg, err := ConfigFromEnv(map[string]string{
		"SMTP_ENABLED":    "true",
		"SMTP_HOST":       "smtp.example.com",
		"SMTP_FROM_EMAIL": "no-reply@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 587 || cfg.Encryption != EncryptionSTARTTLS {
		t.Fatalf("expected starttls on 587, got %s on %d", cfg.Encryption, cfg.Port)
	}
	if cfg.FromName != "Sophus" || cfg.Timeout != 15*time.Second {
		t.Fatalf("unexpected defaults: name=%q timeout=%s", cfg.FromName, cfg.Timeout)
	}
	if cfg.Address() != "smtp.example.com:587" {
		t.Fatalf("unexpected address %q", cfg.Address())
	}
}

func TestConfigFromEnvImplicitTLSDefaultsToPort465(t *testing.T) {
	cfg, err := ConfigFromEnv(map[string]string{
		"SMTP_ENABLED":    "true",
		"SMTP_HOST":       "smtp.example.com",
		"SMTP_FROM_EMAIL": "no-reply@example.com",
		"SMTP_ENCRYPTION": "TLS",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 465 || cfg.Encryption != EncryptionTLS {
		t.Fatalf("expected tls on 465, got %s on %d", cfg.Encryption, cfg.Port)
	}
}

func TestConfigFromEnvRejectsInvalidValues(t *testing.T) {
	base := map[string]string{
		"SMTP_ENABLED":    "true",
		"SMTP_HOST":       "smtp.example.com",
		"SMTP_FROM_EMAIL": "no-reply@example.com",
	}
	cases := map[string]map[string]string{
		"missing host":           {"SMTP_HOST": ""},
		"host with spaces":       {"SMTP_HOST": "smtp example.com"},
		"invalid from email":     {"SMTP_FROM_EMAIL": "nope"},
		"from name with newline": {"SMTP_FROM_NAME": "Sophus\r\nBcc: x@y.com"},
		"unknown encryption":     {"SMTP_ENCRYPTION": "ssl3"},
		"auth without tls":       {"SMTP_ENCRYPTION": "none", "SMTP_USERNAME": "user"},
		"port out of range":      {"SMTP_PORT": "70000"},
		"bad timeout":            {"SMTP_TIMEOUT": "soon"},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			values := map[string]string{}
			for key, value := range base {
				values[key] = value
			}
			for key, value := range override {
				values[key] = value
			}
			if _, err := ConfigFromEnv(values); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestNewSenderWhenDisabledReturnsErrDisabled(t *testing.T) {
	err := NewSender(Config{}).Send(context.Background(), Message{To: "ana@example.com", Subject: "Oi", Text: "Oi"})
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if !strings.Contains(ErrDisabled.Error(), "desativado") {
		t.Fatal("unexpected ErrDisabled message")
	}
}
