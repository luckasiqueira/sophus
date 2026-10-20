package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// Message is one email ready to be sent. HTML and Text are both sent as multipart/alternative.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Sender delivers messages. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// NewSender returns an SMTP sender when the config is enabled, or a sender that
// always returns ErrDisabled. Callers can use the same code path in both cases.
func NewSender(cfg Config) Sender {
	if !cfg.Enabled {
		return disabledSender{}
	}
	return &SMTPSender{cfg: cfg, now: time.Now}
}

type disabledSender struct{}

func (disabledSender) Send(context.Context, Message) error { return ErrDisabled }

// SMTPSender sends messages through an SMTP relay.
type SMTPSender struct {
	cfg Config
	now func() time.Time
}

// Send opens a connection, authenticates when configured and delivers the message.
// Credentials are never sent before TLS is active.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if err := validateRecipient(msg.To); err != nil {
		return err
	}
	messageID, err := newMessageID(s.cfg.FromEmail)
	if err != nil {
		return err
	}
	body, err := buildMessage(s.cfg.FromName, s.cfg.FromEmail, msg, s.now(), messageID)
	if err != nil {
		return err
	}

	dialer := net.Dialer{Timeout: s.cfg.Timeout}
	raw, err := dialer.DialContext(ctx, "tcp", s.cfg.Address())
	if err != nil {
		return fmt.Errorf("conexão SMTP: %w", err)
	}
	if err := raw.SetDeadline(time.Now().Add(s.cfg.Timeout)); err != nil {
		raw.Close()
		return err
	}

	tlsConfig := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn = raw
	if s.cfg.Encryption == EncryptionTLS {
		conn = tls.Client(raw, tlsConfig)
	}
	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("handshake SMTP: %w", err)
	}
	defer client.Close()

	if s.cfg.Encryption == EncryptionSTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("servidor SMTP não suporta STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("autenticação SMTP: %w", err)
		}
	}
	if err := client.Mail(s.cfg.FromEmail); err != nil {
		return fmt.Errorf("remetente recusado: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("destinatário recusado: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("início do envio: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return fmt.Errorf("envio da mensagem: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finalização da mensagem: %w", err)
	}
	return client.Quit()
}

// buildMessage renders RFC 5322 headers and a multipart/alternative body.
func buildMessage(fromName, fromEmail string, msg Message, now time.Time, messageID string) ([]byte, error) {
	if err := rejectLineBreaks(msg.Subject); err != nil {
		return nil, err
	}
	if strings.TrimSpace(msg.HTML) == "" && strings.TrimSpace(msg.Text) == "" {
		return nil, errors.New("mensagem sem conteúdo")
	}
	from := (&mail.Address{Name: fromName, Address: fromEmail}).String()

	var body bytes.Buffer
	boundary, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary(boundary); err != nil {
		return nil, err
	}

	headers := []struct{ key, value string }{
		{"From", from},
		{"To", msg.To},
		{"Subject", mime.QEncoding.Encode("utf-8", msg.Subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"Message-ID", messageID},
		{"MIME-Version", "1.0"},
		{"Content-Type", "multipart/alternative; boundary=" + writer.Boundary()},
	}
	var out bytes.Buffer
	for _, header := range headers {
		out.WriteString(header.key + ": " + header.value + "\r\n")
	}
	out.WriteString("\r\n")

	if strings.TrimSpace(msg.Text) != "" {
		if err := writePart(writer, "text/plain", msg.Text); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(msg.HTML) != "" {
		if err := writePart(writer, "text/html", msg.HTML); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

func writePart(writer *multipart.Writer, contentType, content string) error {
	part, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType + "; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	encoder := quotedprintable.NewWriter(part)
	if _, err := encoder.Write([]byte(content)); err != nil {
		return err
	}
	return encoder.Close()
}

func validateRecipient(address string) error {
	if err := rejectLineBreaks(address); err != nil {
		return err
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address {
		return errors.New("destinatário inválido")
	}
	return nil
}

func rejectLineBreaks(value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("caractere inválido em cabeçalho de email")
	}
	return nil
}

func newMessageID(fromEmail string) (string, error) {
	id, err := randomHex(16)
	if err != nil {
		return "", err
	}
	domain := fromEmail[strings.LastIndex(fromEmail, "@")+1:]
	return "<" + id + "@" + domain + ">", nil
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
