package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"freipadel/internal/store"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	sessionCookie       = "fp_session"
	sessionLifetime     = 30 * 24 * time.Hour
	emailChangeTTL      = 8 * time.Hour
	emailChangeCooldown = time.Minute
)

type User struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

type Me struct {
	User           User `json:"user"`
	EmailerEnabled bool `json:"emailer_enabled"`
}

// requireAuth wraps a handler and resolves the current user from the session cookie.
func (a *App) requireAuth(next func(w http.ResponseWriter, r *http.Request, u *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := a.userFromRequest(r)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next(w, r, u)
	}
}

// requireAdmin is requireAuth plus an admin check.
func (a *App) requireAdmin(next func(w http.ResponseWriter, r *http.Request, u *User)) http.HandlerFunc {
	return a.requireAuth(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !u.IsAdmin {
			httpError(w, http.StatusForbidden, "admin only")
			return
		}
		next(w, r, u)
	})
}

func (a *App) userFromRequest(r *http.Request) (*User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	record, err := store.FindUserBySession(a.orm.WithContext(r.Context()), hashToken(c.Value))
	if err != nil {
		return nil, errors.New("invalid session")
	}
	return &User{ID: record.ID, Email: record.Email, Name: record.Name, IsAdmin: record.IsAdmin}, nil
}

func (a *App) createSession(w http.ResponseWriter, userID int64) error {
	token := randomToken(32)
	expires := time.Now().UTC().Add(sessionLifetime)
	// Store only the hash; the raw token is handed to the browser via the cookie below.
	if err := store.CreateSession(a.orm, hashToken(token), userID, expires.Format("2006-01-02 15:04:05")); err != nil {
		return err
	}
	a.setSessionCookie(w, token, expires)
	return nil
}

func (a *App) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies,
	})
}

// GET /api/auth/setup — whether the very first user still needs to be created.
func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	count, err := store.CountUsers(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": count == 0})
}

// POST /api/auth/register
func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteToken string `json:"invite_token"`
		Email       string `json:"email"`
		Name        string `json:"name"`
		Password    string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	var err error
	req.Email, err = normalizeEmail(req.Email)
	req.Name = strings.TrimSpace(req.Name)

	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if req.Name == "" {
		httpError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Password) < 8 {
		httpError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	token := randomToken(32)
	expires := time.Now().UTC().Add(sessionLifetime)
	var created store.UserRecord
	var firstUser bool
	var responseStatus int
	var responseMessage string
	accountConflict := false
	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		userCount, err := store.CountUsers(tx)
		if err != nil {
			return err
		}
		firstUser = userCount == 0
		if !firstUser {
			invite, err := store.FindInvite(tx, req.InviteToken)
			if store.IsNotFound(err) {
				responseStatus, responseMessage = http.StatusForbidden, "invalid invite link"
				return errors.New(responseMessage)
			}
			if err != nil {
				return err
			}
			if invite.Disabled {
				responseStatus, responseMessage = http.StatusForbidden, "this invite link has been disabled"
				return errors.New(responseMessage)
			}
			if invite.Kind != "group" && invite.UsedByID != nil {
				responseStatus, responseMessage = http.StatusForbidden, "this invite link has already been used"
				return errors.New(responseMessage)
			}
			if invite.Kind == "email" && (invite.Email == nil || req.Email != *invite.Email) {
				responseStatus, responseMessage = http.StatusForbidden, "this invite belongs to another email"
				return errors.New(responseMessage)
			}
		}
		reserved, err := store.PendingEmailChangeEmailExists(tx, req.Email, 0)
		if err != nil {
			return err
		}
		if reserved {
			accountConflict = true
			return errors.New("email reserved by pending change")
		}

		created, err = store.CreateUser(tx, req.Email, req.Name, string(hash), firstUser)
		if err != nil {
			accountConflict = true
			return err
		}
		if err := store.CreateDefaultSettings(tx, created.ID); err != nil {
			return err
		}
		memberPayload, _ := json.Marshal(syncMember{ID: created.ID, Name: created.Name, IsAdmin: created.IsAdmin})
		if err := store.AppendSync(tx, "user", strconv.FormatInt(created.ID, 10), "upsert", memberPayload, 0); err != nil {
			return err
		}
		if !firstUser {
			if err := store.RedeemInvite(tx, req.InviteToken, created.ID); err != nil {
				return err
			}
			invite, err := store.FindInvite(tx, req.InviteToken)
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(inviteFromRecord(invite))
			if err := store.AppendSync(tx, "invite", invite.Token, "upsert", payload, visibleToAdmins); err != nil {
				return err
			}
		}
		return store.CreateSession(tx, hashToken(token), created.ID, expires.Format("2006-01-02 15:04:05"))
	})
	if responseStatus != 0 {
		httpError(w, responseStatus, responseMessage)
		return
	}
	if err != nil {
		if accountConflict {
			httpError(w, http.StatusConflict, "an account with this email already exists")
		} else {
			httpError(w, http.StatusInternalServerError, "database error")
		}
		return
	}
	a.hub.notify()
	a.setSessionCookie(w, token, expires)
	writeJSON(w, http.StatusCreated, Me{
		User:           User{ID: created.ID, Email: created.Email, Name: created.Name, IsAdmin: created.IsAdmin},
		EmailerEnabled: a.emailer.Configured()},
	)

	// notify admin via telegram
	a.telegramSender.SendMsg(a.scrapeCfg.Telegram.AdminChatID, fmt.Sprint("New FreiPadel registration: ", req.Name))
}

// POST /api/auth/login
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Email, _ = normalizeEmail(req.Email)

	record, err := store.FindUserByEmail(a.orm.WithContext(r.Context()), req.Email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(req.Password)) != nil {
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	u := User{ID: record.ID, Email: record.Email, Name: record.Name, IsAdmin: record.IsAdmin}

	if err := a.createSession(w, u.ID); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}

	me := Me{
		User:           u,
		EmailerEnabled: a.emailer.Configured(),
	}
	writeJSON(w, http.StatusOK, me)
}

// POST /api/auth/logout
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = store.DeleteSession(a.orm.WithContext(r.Context()), hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/auth/me
func (a *App) handleMe(w http.ResponseWriter, r *http.Request, u *User) {
	me := Me{
		User:           *u,
		EmailerEnabled: a.emailer.Configured(),
	}
	writeJSON(w, http.StatusOK, me)
}

type emailChangeStatus struct {
	PendingEmail *string `json:"pending_email"`
	ExpiresAt    *string `json:"expires_at"`
}

// GET /api/auth/email-change — returns the current user's pending request.
func (a *App) handleGetEmailChange(w http.ResponseWriter, r *http.Request, u *User) {
	pending, err := store.FindPendingEmailChangeByUserID(a.orm.WithContext(r.Context()), u.ID)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusOK, emailChangeStatus{})
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, emailChangeStatus{
		PendingEmail: &pending.NewEmail,
		ExpiresAt:    &pending.ExpiresAt,
	})
}

// POST /api/auth/email-change — creates a pending request. Authentication of
// the current session authorizes the request; the user does not re-enter their
// password. The account continues to use its current address until confirmed.
func (a *App) handleRequestEmailChange(w http.ResponseWriter, r *http.Request, u *User) {
	if !a.emailer.Configured() {
		httpError(w, http.StatusServiceUnavailable, "email changes are unavailable in this deployment")
		return
	}

	var req struct {
		NewEmail string `json:"new_email"`
		Origin   string `json:"origin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	newEmail, err := normalizeEmail(req.NewEmail)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if newEmail == u.Email {
		httpError(w, http.StatusBadRequest, "this is already your email address")
		return
	}
	origin := a.linkOrigin(req.Origin)
	if origin == "" {
		httpError(w, http.StatusBadRequest, "origin is required")
		return
	}

	token := randomToken(32)
	expires := time.Now().UTC().Add(emailChangeTTL)
	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.DeleteExpiredPendingEmailChanges(tx); err != nil {
			return err
		}
		if pending, err := store.FindPendingEmailChangeByUserID(tx, u.ID); err == nil {
			requestedAt, parseErr := time.ParseInLocation("2006-01-02 15:04:05", pending.RequestedAt, time.UTC)
			if parseErr == nil && requestedAt.Add(emailChangeCooldown).After(time.Now().UTC()) {
				return errEmailChangeRateLimited
			}
		} else if !store.IsNotFound(err) {
			return err
		}
		unavailable, err := store.EmailUnavailable(tx, newEmail, u.ID)
		if err != nil {
			return err
		}
		if unavailable {
			return errEmailUnavailable
		}
		return store.UpsertPendingEmailChange(tx, u.ID, newEmail, hashToken(token), expires)
	})
	if errors.Is(err, errEmailUnavailable) {
		httpError(w, http.StatusConflict, "this email address is unavailable")
		return
	}
	if errors.Is(err, errEmailChangeRateLimited) {
		w.Header().Set("Retry-After", "60")
		httpError(w, http.StatusTooManyRequests, "please wait before requesting another confirmation email")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	confirmationURL := origin + "/confirm-email?token=" + url.QueryEscape(token)
	body := fmt.Sprintf(
		`<p>A request was made to use this email address for a FreiPadel account.</p>
<p><a href="%s">Confirm email change</a></p>
<p>This link expires in 8 hours. If you did not request this, you can ignore this email.</p>`,
		template.HTMLEscapeString(confirmationURL),
	)
	if err := a.emailer.Send(newEmail, "Confirm your new FreiPadel email address", body); err != nil {
		// Keep the request valid: SMTP may have accepted the message before
		// reporting an error, and a subsequent request will rotate the token.
		log.Printf("send email-change confirmation for user %d: %v", u.ID, err)
		httpError(w, http.StatusBadGateway, "could not send confirmation email")
		return
	}

	oldAddressBody := fmt.Sprintf(
		`<p>A request was made to change your FreiPadel email address to %s.</p>
<p>Your account still uses this address until the new address is confirmed. If you did not request this, contact an administrator.</p>`,
		template.HTMLEscapeString(newEmail),
	)
	if err := a.emailer.Send(u.Email, "FreiPadel email change requested", oldAddressBody); err != nil {
		log.Printf("send email-change notice for user %d: %v", u.ID, err)
	}

	writeJSON(w, http.StatusAccepted, emailChangeStatus{
		PendingEmail: &newEmail,
		ExpiresAt:    stringPointer(expires.Format("2006-01-02 15:04:05")),
	})
}

// DELETE /api/auth/email-change — cancels the current user's pending request.
func (a *App) handleCancelEmailChange(w http.ResponseWriter, r *http.Request, u *User) {
	if err := store.DeletePendingEmailChangeForUser(a.orm.WithContext(r.Context()), u.ID); err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/auth/email-change/confirm — consumes a token received at the new
// address. This route is public so the link works on a different device.
func (a *App) handleConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Token == "" {
		httpError(w, http.StatusBadRequest, "invalid or expired confirmation link")
		return
	}

	var changedEmail string
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		tokenHash := hashToken(req.Token)
		pending, err := store.FindPendingEmailChangeByTokenHash(tx, tokenHash)
		if store.IsNotFound(err) {
			return errInvalidEmailChangeToken
		}
		if err != nil {
			return err
		}
		expires, err := time.ParseInLocation("2006-01-02 15:04:05", pending.ExpiresAt, time.UTC)
		if err != nil || !expires.After(time.Now().UTC()) {
			return errInvalidEmailChangeToken
		}
		unavailable, err := store.EmailUnavailable(tx, pending.NewEmail, pending.UserID)
		if err != nil {
			return err
		}
		if unavailable {
			return errEmailUnavailable
		}
		if affected, err := store.UpdateUserEmail(tx, pending.UserID, pending.NewEmail); err != nil {
			return err
		} else if affected != 1 {
			return errInvalidEmailChangeToken
		}
		if err := store.DeletePendingEmailChangeByTokenHash(tx, tokenHash); err != nil {
			return err
		}
		changedEmail = pending.NewEmail
		return nil
	})
	if errors.Is(err, errInvalidEmailChangeToken) {
		httpError(w, http.StatusBadRequest, "invalid or expired confirmation link")
		return
	}
	if errors.Is(err, errEmailUnavailable) {
		httpError(w, http.StatusConflict, "this email address is no longer available")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": changedEmail})
}

var (
	errEmailUnavailable        = errors.New("email unavailable")
	errEmailChangeRateLimited  = errors.New("email change rate limited")
	errInvalidEmailChangeToken = errors.New("invalid email change token")
)

func stringPointer(value string) *string { return &value }
