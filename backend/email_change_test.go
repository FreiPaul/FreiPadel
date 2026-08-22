package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"freipadel/internal/store"
)

type sentEmail struct {
	to      string
	subject string
	body    string
}

type fakeMailSender struct {
	configured bool
	sent       []sentEmail
	err        error
}

func (f *fakeMailSender) Configured() bool { return f.configured }

func (f *fakeMailSender) Send(to, subject, body string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentEmail{to: to, subject: subject, body: body})
	return nil
}

func TestEmailChangeRequiresConfirmationBeforeUpdatingUser(t *testing.T) {
	storage, err := store.Open(filepath.Join(t.TempDir(), "email-change.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	record, err := store.CreateUser(storage.ORM, "old@example.com", "Player", "password-hash", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	mailer := &fakeMailSender{configured: true}
	app := &App{orm: storage.ORM, emailer: mailer}
	user := &User{ID: record.ID, Email: record.Email, Name: record.Name}

	request := httptest.NewRequest(http.MethodPost, "/api/auth/email-change", bytes.NewBufferString(`{"new_email":"NEW@example.com","origin":"https://example.test/"}`))
	response := httptest.NewRecorder()
	app.handleRequestEmailChange(response, request, user)
	if response.Code != http.StatusAccepted {
		t.Fatalf("request status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(mailer.sent) != 2 || mailer.sent[0].to != "new@example.com" || mailer.sent[1].to != "old@example.com" {
		t.Fatalf("sent emails = %#v", mailer.sent)
	}
	if !regexp.MustCompile(`href="https://example\.test/confirm-email\?token=`).MatchString(mailer.sent[0].body) {
		t.Fatalf("confirmation email does not use request origin: %q", mailer.sent[0].body)
	}
	repeatRequest := httptest.NewRequest(http.MethodPost, "/api/auth/email-change", bytes.NewBufferString(`{"new_email":"other@example.com","origin":"https://example.test"}`))
	repeatResponse := httptest.NewRecorder()
	app.handleRequestEmailChange(repeatResponse, repeatRequest, user)
	if repeatResponse.Code != http.StatusTooManyRequests || repeatResponse.Header().Get("Retry-After") != "60" {
		t.Fatalf("immediate repeat status = %d, retry-after = %q, body = %s", repeatResponse.Code, repeatResponse.Header().Get("Retry-After"), repeatResponse.Body.String())
	}
	if len(mailer.sent) != 2 {
		t.Fatalf("rate-limited request sent emails = %#v", mailer.sent)
	}

	unchanged, err := store.FindUserByEmail(storage.ORM, "old@example.com")
	if err != nil || unchanged.ID != record.ID {
		t.Fatalf("old email before confirmation: user = %#v, error = %v", unchanged, err)
	}
	if _, err := store.FindUserByEmail(storage.ORM, "new@example.com"); !store.IsNotFound(err) {
		t.Fatalf("new email before confirmation lookup error = %v, want not found", err)
	}

	tokenMatch := regexp.MustCompile(`token=([0-9a-f]+)`).FindStringSubmatch(mailer.sent[0].body)
	if len(tokenMatch) != 2 {
		t.Fatalf("confirmation token missing from email body %q", mailer.sent[0].body)
	}
	token := tokenMatch[1]
	pending, err := store.FindPendingEmailChangeByUserID(storage.ORM, record.ID)
	if err != nil {
		t.Fatalf("find pending email change: %v", err)
	}
	if pending.TokenHash == token || pending.TokenHash != hashToken(token) {
		t.Fatalf("stored token %q is not the expected hash", pending.TokenHash)
	}
	if expires, err := time.ParseInLocation("2006-01-02 15:04:05", pending.ExpiresAt, time.UTC); err != nil || time.Until(expires) < 7*time.Hour {
		t.Fatalf("pending expiry = %q, error = %v", pending.ExpiresAt, err)
	}

	confirm := httptest.NewRequest(http.MethodPost, "/api/auth/email-change/confirm", bytes.NewBufferString(`{"token":"`+token+`"}`))
	confirmResponse := httptest.NewRecorder()
	app.handleConfirmEmailChange(confirmResponse, confirm)
	if confirmResponse.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmResponse.Code, confirmResponse.Body.String())
	}
	changed, err := store.FindUserByEmail(storage.ORM, "new@example.com")
	if err != nil || changed.ID != record.ID {
		t.Fatalf("new email after confirmation: user = %#v, error = %v", changed, err)
	}
	if _, err := store.FindPendingEmailChangeByUserID(storage.ORM, record.ID); !store.IsNotFound(err) {
		t.Fatalf("consumed pending lookup error = %v, want not found", err)
	}

	reuse := httptest.NewRequest(http.MethodPost, "/api/auth/email-change/confirm", bytes.NewBufferString(`{"token":"`+token+`"}`))
	reuseResponse := httptest.NewRecorder()
	app.handleConfirmEmailChange(reuseResponse, reuse)
	if reuseResponse.Code != http.StatusBadRequest {
		t.Fatalf("reused token status = %d, body = %s", reuseResponse.Code, reuseResponse.Body.String())
	}
}

func TestEmailChangeRequestRequiresAuthenticatedSession(t *testing.T) {
	mailer := &fakeMailSender{configured: true}
	app := &App{emailer: mailer}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/email-change", bytes.NewBufferString(`{"new_email":"new@example.com"}`))
	response := httptest.NewRecorder()

	app.requireAuth(app.handleRequestEmailChange).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(mailer.sent) != 0 {
		t.Fatalf("unauthenticated request sent emails = %#v", mailer.sent)
	}
}
