// Package emailer sends transactional emails over SMTP. Credentials come from
// the environment (SMTP_HOST / SMTP_PORT / SMTP_USER / SMTP_PASS / MAIL_FROM),
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
}

// NewSender builds an Emailer from explicit SMTP settings. If port is empty it
// defaults to 587 (submission + STARTTLS); if from is empty it falls back to
// username.
func NewSender(host, port, username, password, from string) *Emailer {
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = username
	}
	return &Emailer{host: host, port: port, username: username, password: password, from: from}
}

// FromEnv builds an Emailer from the SMTP_* / MAIL_FROM environment variables.
func FromEnv() *Emailer {
	return NewSender(
		os.Getenv("SMTP_HOST"),
		os.Getenv("SMTP_PORT"),
		os.Getenv("SMTP_USER"),
		os.Getenv("SMTP_PASS"),
		os.Getenv("MAIL_FROM"),
	)
}

// Configured reports whether enough SMTP settings are present to send mail.
func (e *Emailer) Configured() bool {
	return e.host != "" && e.username != "" && e.password != ""
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
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
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
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	c, err := smtp.NewClient(conn, e.host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if err := c.StartTLS(&tls.Config{ServerName: e.host}); err != nil {
		return fmt.Errorf("smtp starttls: %w", err)
	}
	if err := c.Auth(smtp.PlainAuth("", e.username, e.password, e.host)); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
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
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return c.Quit()
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
