package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie   = "fp_session"
	sessionLifetime = 30 * 24 * time.Hour
)

type User struct {
	ID      int64  `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
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
	var u User
	var isAdmin int
	err = a.db.QueryRow(`
		SELECT u.id, u.email, u.name, u.is_admin
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > datetime('now')`,
		hashToken(c.Value)).Scan(&u.ID, &u.Email, &u.Name, &isAdmin)
	if err != nil {
		return nil, errors.New("invalid session")
	}
	u.IsAdmin = isAdmin == 1
	return &u, nil
}

func (a *App) createSession(w http.ResponseWriter, userID int64) error {
	token := randomToken(32)
	expires := time.Now().UTC().Add(sessionLifetime)
	// Store only the hash; the raw token is handed to the browser via the cookie below.
	_, err := a.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), userID, expires.Format("2006-01-02 15:04:05"))
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secureCookies,
	})
	return nil
}

// GET /api/auth/setup — whether the very first user still needs to be created.
func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	var count int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
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
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if _, err := mail.ParseAddress(req.Email); err != nil {
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

	var userCount int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	firstUser := userCount == 0

	// Everyone except the very first user needs a valid invite: a one-time
	// link that is still unused, or a group link that is not disabled.
	if !firstUser {
		var used sql.NullInt64
		var kind string
		var disabled int
		err := a.db.QueryRow(`SELECT used_by, kind, disabled FROM invites WHERE token = ?`,
			req.InviteToken).Scan(&used, &kind, &disabled)
		if err == sql.ErrNoRows {
			httpError(w, http.StatusForbidden, "invalid invite link")
			return
		}
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		if disabled == 1 {
			httpError(w, http.StatusForbidden, "this invite link has been disabled")
			return
		}
		if kind != "group" && used.Valid {
			httpError(w, http.StatusForbidden, "this invite link has already been used")
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	isAdmin := 0
	if firstUser {
		isAdmin = 1
	}
	res, err := a.db.Exec(`INSERT INTO users (email, name, password_hash, is_admin) VALUES (?, ?, ?, ?)`,
		req.Email, req.Name, string(hash), isAdmin)
	if err != nil {
		httpError(w, http.StatusConflict, "an account with this email already exists")
		return
	}
	userID, _ := res.LastInsertId()

	// Default availability settings (weekdays 19:00–21:00).
	_, _ = a.db.Exec(`INSERT INTO user_settings (user_id) VALUES (?)`, userID)

	_ = appendSync(a.db, "user", strconv.FormatInt(userID, 10), "upsert",
		syncMember{ID: userID, Name: req.Name, IsAdmin: firstUser}, 0)

	if !firstUser {
		// Group invites just count registrations; one-time invites are marked used.
		_, _ = a.db.Exec(`UPDATE invites SET
				uses = uses + 1,
				used_by = CASE WHEN kind = 'group' THEN used_by ELSE ? END,
				used_at = CASE WHEN kind = 'group' THEN used_at ELSE datetime('now') END
			WHERE token = ?`,
			userID, req.InviteToken)
		// Admins see the invite flip to used/counted live.
		if inv, err := loadInvite(a.db, req.InviteToken); err == nil {
			_ = appendSync(a.db, "invite", inv.Token, "upsert", inv, visibleToAdmins)
		}
	}
	// One notify after all deltas of this registration are in the log.
	a.hub.notify()

	if err := a.createSession(w, userID); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, http.StatusCreated, User{ID: userID, Email: req.Email, Name: req.Name, IsAdmin: firstUser})

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
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	var u User
	var hash string
	var isAdmin int
	err := a.db.QueryRow(`SELECT id, email, name, password_hash, is_admin FROM users WHERE email = ?`,
		req.Email).Scan(&u.ID, &u.Email, &u.Name, &hash, &isAdmin)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		httpError(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	u.IsAdmin = isAdmin == 1

	if err := a.createSession(w, u.ID); err != nil {
		httpError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// POST /api/auth/logout
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token = ?`, hashToken(c.Value))
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
	writeJSON(w, http.StatusOK, u)
}
