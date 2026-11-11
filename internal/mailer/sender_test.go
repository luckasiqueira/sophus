package mailer

import (
	"bufio"
	"context"
	"mime"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeSMTP accepts one plain-text session and records the DATA payload.
func fakeSMTP(t *testing.T) (port int, received <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	out := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(conn)
		write := func(line string) { conn.Write([]byte(line + "\r\n")) }
		write("220 localhost ESMTP")
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			command := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
				write("250 localhost")
			case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
				write("250 OK")
			case command == "DATA":
				write("354 go ahead")
				var data strings.Builder
				for {
					dataLine, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if dataLine == ".\r\n" {
						break
					}
					data.WriteString(dataLine)
				}
				out <- data.String()
				write("250 queued")
			case command == "QUIT":
				write("221 bye")
				return
			default:
				write("502 unexpected")
			}
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, out
}

func TestSMTPSenderDeliversMultipartMessage(t *testing.T) {
	port, received := fakeSMTP(t)
	sender := &SMTPSender{
		cfg: Config{
			Enabled:    true,
			Host:       "127.0.0.1",
			Port:       port,
			FromEmail:  "no-reply@example.com",
			FromName:   "DoveChat",
			Encryption: EncryptionNone,
			Timeout:    5 * time.Second,
		},
		now: func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) },
	}

	err := sender.Send(context.Background(), Message{
		To:      "ana@example.com",
		Subject: "Pagamento confirmado",
		HTML:    "<p>Olá, <strong>Ana</strong></p>",
		Text:    "Olá, Ana",
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	payload := <-received
	for _, want := range []string{
		`From: "DoveChat" <no-reply@example.com>`,
		"To: ana@example.com",
		"MIME-Version: 1.0",
		"multipart/alternative",
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
		"Message-ID: <",
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
	subjectLine := ""
	for _, line := range strings.Split(payload, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subjectLine = strings.TrimPrefix(line, "Subject: ")
		}
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(subjectLine)
	if err != nil || decoded != "Pagamento confirmado" {
		t.Fatalf("subject not preserved: %q (%v)", decoded, err)
	}
}

func TestSMTPSenderRejectsInjectedHeaders(t *testing.T) {
	sender := &SMTPSender{cfg: Config{Enabled: true, Host: "127.0.0.1", Port: 1, FromEmail: "no-reply@example.com"}, now: time.Now}
	cases := map[string]Message{
		"recipient with CRLF": {To: "ana@example.com\r\nBcc: spy@example.com", Subject: "Oi", Text: "Oi"},
		"subject with CRLF":   {To: "ana@example.com", Subject: "Oi\r\nBcc: spy@example.com", Text: "Oi"},
		"empty body":          {To: "ana@example.com", Subject: "Oi"},
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := sender.Send(context.Background(), msg); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
