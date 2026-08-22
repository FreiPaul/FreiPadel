// Package emailer sends transactional emails over SMTP. Credentials come from
// the environment (SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / MAIL_FROM /
// SMTP_INSECURE),
// set via the .env file for local dev or the Dockerfile/compose environment
// block in production.
//
// The send path mirrors the NL contact server: an explicit smtp.Client with a
// dial timeout, mandatory StartTLS, and headers built defensively (sanitized
// values, MIME-encoded subject, Message-ID) so strict mailservers and spam
// filters (e.g. rspamd) accept the mail.
package emailer

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// fromName is the display name used in the From header.
const fromName = "FreiPadel"

type Emailer struct {
	host     string
	port     string
	username string
	password string
	from     string // envelope + header sender; defaults to username
	insecure bool   // opt out of STARTTLS on non-465 ports; for local test servers only
	enabled  bool
}

// NewSender builds an Emailer from explicit SMTP settings. If port is empty it
// defaults to 587 (submission + STARTTLS); if from is empty it falls back to
// username. Transport security is on by default: port 465 dials implicit TLS,
// every other port requires STARTTLS unless insecure is set.
func NewSender(host, port, username, password, from string, insecure, enabled bool) *Emailer {
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = username
	}
	return &Emailer{host: host, port: port, username: username, password: password, from: from, insecure: insecure, enabled: enabled}
}

// FromEnv builds an Emailer from the SMTP_* / MAIL_FROM environment variables.
func FromEnv() *Emailer {
	if os.Getenv("EMAILER_ENABLED") == "1" {
		return NewSender(
			os.Getenv("SMTP_HOST"),
			os.Getenv("SMTP_PORT"),
			os.Getenv("SMTP_USER"),
			os.Getenv("SMTP_PASS"),
			os.Getenv("MAIL_FROM"),
			os.Getenv("SMTP_INSECURE") == "1",
			true,
		)
	} else {
		return NewSender(
			"",
			"",
			"",
			"",
			"",
			false,
			false,
		)
	}
}

// Configured reports whether enough SMTP settings are present to send mail.
func (e *Emailer) Configured() bool {
	return e.host != "" && e.enabled
}

// Send delivers a plain-text UTF-8 email. Like the telegram sender, it is a
// no-op (returns nil) when the emailer is unconfigured or there is no
// recipient, so deployments without SMTP set up don't error.
func (e *Emailer) Send(to, subject, body string) error {
	if !e.Configured() || strings.TrimSpace(to) == "" {
		return nil // abort without error
	}

	// Validate + normalize the recipient to a bare addr-spec. This rejects
	// embedded display names, address lists, and control characters — the
	// basis against To-header injection.
	addr, err := mail.ParseAddress(to)
	if err != nil || addr.Name != "" {
		return fmt.Errorf("invalid recipient %q", to)
	}
	to = addr.Address

	msg := e.buildMessage(to, subject, body)
	return e.sendSMTP(to, msg)
}

// buildMessage assembles an RFC 5322 message with CRLF line endings.
func (e *Emailer) buildMessage(to, subject, body string) []byte {
	from := mail.Address{Name: fromName, Address: e.from}

	var b strings.Builder
	// Date + Message-ID are effectively required — spam filters (rspamd) score
	// mail without them higher. Non-ASCII header values must be MIME-encoded.
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + messageID(e.from) + "\r\n")
	b.WriteString("From: " + from.String() + "\r\n")
	b.WriteString("To: " + sanitizeHeader(to) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", sanitizeHeader(subject)) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)

	// Normalize line endings to CRLF: bare \n from templates/textareas is
	// rejected by strict mailservers.
	s := b.String()
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "\r\n")
	return []byte(s)
}

// sendSMTP delivers msg via an explicit client with a dial timeout and
// mandatory StartTLS.
func (e *Emailer) sendSMTP(to string, msg []byte) error {
	addr := net.JoinHostPort(e.host, e.port)

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tlsConfig := &tls.Config{
		ServerName: e.host,
		MinVersion: tls.VersionTLS12,
	}

	var (
		conn net.Conn
		err  error
	)

	if e.port == "465" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}

	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	// Prevent reads or writes from hanging indefinitely.
	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		conn.Close()
		return fmt.Errorf("smtp deadline: %w", err)
	}

	c, err := smtp.NewClient(conn, e.host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if e.port != "465" && !e.insecure {
		ok, _ := c.Extension("STARTTLS")
		if !ok {
			return fmt.Errorf("smtp server does not advertise STARTTLS")
		}

		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	if e.username != "" || e.password != "" {
		if e.username == "" || e.password == "" {
			return fmt.Errorf("SMTP_USER and SMTP_PASS must either both be set or both be empty")
		}

		if err := c.Auth(smtp.PlainAuth("", e.username, e.password, e.host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(e.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}

	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	if _, err := wc.Write(msg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}

	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

// messageID builds a unique Message-ID using the sender's domain.
func messageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		domain = from[at+1:]
	}
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "<" + hex.EncodeToString(buf[:]) + "@" + domain + ">"
}

// sanitizeHeader strips CR/LF from a header value to prevent header injection.
func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	return strings.TrimSpace(s)
}
