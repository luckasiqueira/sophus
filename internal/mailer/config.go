// Package mailer sends transactional emails through SMTP.
package mailer

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// Encryption selects how the SMTP connection is secured.
type Encryption string

const (
	// EncryptionSTARTTLS upgrades a plain connection with STARTTLS (usually port 587).
	EncryptionSTARTTLS Encryption = "starttls"
	// EncryptionTLS connects with implicit TLS (usually port 465).
	EncryptionTLS Encryption = "tls"
	// EncryptionNone sends in clear text. It is only meant for local relays and tests.
	EncryptionNone Encryption = "none"
)

// ErrDisabled is returned when SMTP_ENABLED is off. Callers should log it and move on.
var ErrDisabled = errors.New("envio de email desativado")

// Config holds the SMTP settings loaded from the environment.
type Config struct {
	Enabled    bool
	Host       string
	Port       int
	Username   string
	Password   string
	FromEmail  string
	FromName   string
	Encryption Encryption
	Timeout    time.Duration
}

// ConfigFromEnv reads SMTP_* keys from values. Validation only runs when SMTP_ENABLED is true,
// so a deployment without SMTP keeps working and payments are never blocked by email.
//
//	SMTP_ENABLED     true | false (default false)
//	SMTP_HOST        servidor SMTP
//	SMTP_PORT        porta (default 587 with starttls, 465 with tls, 25 with none)
//	SMTP_USERNAME    usuário (opcional, exige TLS)
//	SMTP_PASSWORD    senha (opcional, exige TLS)
//	SMTP_FROM_EMAIL  remetente, obrigatório
//	SMTP_FROM_NAME   nome do remetente (default "DoveChat")
//	SMTP_ENCRYPTION  starttls | tls | none (default starttls)
//	SMTP_TIMEOUT     tempo máximo por envio, ex.: 15s (default 15s)
func ConfigFromEnv(values map[string]string) (Config, error) {
	get := func(key string) string { return strings.TrimSpace(values[key]) }

	cfg := Config{
		Enabled:    parseBool(get("SMTP_ENABLED")),
		Host:       get("SMTP_HOST"),
		Username:   get("SMTP_USERNAME"),
		Password:   values["SMTP_PASSWORD"],
		FromEmail:  get("SMTP_FROM_EMAIL"),
		FromName:   get("SMTP_FROM_NAME"),
		Encryption: Encryption(strings.ToLower(get("SMTP_ENCRYPTION"))),
		Timeout:    15 * time.Second,
	}
	if cfg.FromName == "" {
		cfg.FromName = "DoveChat"
	}
	if cfg.Encryption == "" {
		cfg.Encryption = EncryptionSTARTTLS
	}
	if !cfg.Enabled {
		return cfg, nil
	}

	switch cfg.Encryption {
	case EncryptionSTARTTLS, EncryptionTLS, EncryptionNone:
	default:
		return Config{}, fmt.Errorf("SMTP_ENCRYPTION inválido: use starttls, tls ou none")
	}
	if cfg.Host == "" {
		return Config{}, errors.New("SMTP_HOST não configurado")
	}
	if strings.ContainsAny(cfg.Host, " /\\\t\r\n") {
		return Config{}, errors.New("SMTP_HOST inválido")
	}
	if _, err := mail.ParseAddress(cfg.FromEmail); err != nil {
		return Config{}, errors.New("SMTP_FROM_EMAIL inválido")
	}
	if strings.ContainsAny(cfg.FromName, "\r\n") {
		return Config{}, errors.New("SMTP_FROM_NAME inválido")
	}
	if cfg.Encryption == EncryptionNone && cfg.Username != "" {
		return Config{}, errors.New("autenticação SMTP exige SMTP_ENCRYPTION starttls ou tls")
	}

	cfg.Port = defaultPort(cfg.Encryption)
	if raw := get("SMTP_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port <= 0 || port > 65535 {
			return Config{}, errors.New("SMTP_PORT inválida")
		}
		cfg.Port = port
	}
	if raw := get("SMTP_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil || timeout <= 0 {
			return Config{}, errors.New("SMTP_TIMEOUT inválido, ex.: 15s")
		}
		cfg.Timeout = timeout
	}
	return cfg, nil
}

// Address returns host:port for dialing.
func (cfg Config) Address() string {
	return net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
}

func defaultPort(encryption Encryption) int {
	switch encryption {
	case EncryptionTLS:
		return 465
	case EncryptionNone:
		return 25
	default:
		return 587
	}
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
