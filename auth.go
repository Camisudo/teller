package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const tokenTTL = time.Hour

const adminEmail = "admin@teller.local"

type ctxUserKey struct{}

type userRef struct {
	ID        string `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type userOut struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	Role        string `json:"role"`
	Avatar      string `json:"avatar"`
	Permissions []any  `json:"permissions"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

func (a *app) issueToken(userID string) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.jwtKey)
}

func (a *app) authenticate(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", errUnauthorized("Missing bearer token")
	}
	claims := &jwt.RegisteredClaims{}
	tok, err := jwt.ParseWithClaims(strings.TrimPrefix(h, "Bearer "), claims,
		func(t *jwt.Token) (any, error) { return a.jwtKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !tok.Valid || claims.Subject == "" {
		return "", errUnauthorized("Invalid or expired token")
	}
	return claims.Subject, nil
}

func (a *app) protect(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := a.authenticate(r)
		if err != nil {
			writeError(w, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, uid))
		if err := h(w, r); err != nil {
			writeError(w, err)
		}
	}
}

func (a *app) handle(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeError(w, err)
		}
	}
}

func currentUserID(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserKey{}).(string)
	return v
}

const userCols = `SELECT id, email, first_name, last_name, role, avatar, created_at, updated_at FROM users`

func (a *app) loadUser(ctx context.Context, id string) (userOut, error) {
	var u userOut
	err := a.db.QueryRowContext(ctx, userCols+" WHERE id = ?", id).Scan(
		&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Avatar, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound("User")
	}
	u.Permissions = []any{}
	return u, err
}

// userRefFor validates that a user id exists and returns its reference.
func (a *app) userRefFor(ctx context.Context, id, field string) (userRef, error) {
	var u userRef
	err := a.db.QueryRowContext(ctx, `SELECT id, first_name, last_name FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.FirstName, &u.LastName)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errValidation(field, "User not found")
	}
	return u, err
}

func (a *app) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !emailRe.MatchString(email) {
		return errValidation("email", "Invalid email format")
	}
	var id, hash string
	err := a.db.QueryRowContext(r.Context(), `SELECT id, password_hash FROM users WHERE email = ?`, email).
		Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return errUnauthorized("Invalid credentials")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return errUnauthorized("Invalid credentials")
	}
	token, err := a.issueToken(id)
	if err != nil {
		return err
	}
	u, err := a.loadUser(r.Context(), id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":      u,
		"token":     token,
		"expiresIn": int(tokenTTL.Seconds()),
	})
	return nil
}

// logout is stateless: tokens stay valid until they expire (no revocation list yet).
func (a *app) logout(w http.ResponseWriter, r *http.Request) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *app) refresh(w http.ResponseWriter, r *http.Request) error {
	token, err := a.issueToken(currentUserID(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresIn": int(tokenTTL.Seconds())})
	return nil
}

func (a *app) passwordReset(w http.ResponseWriter, r *http.Request) error {
	return &apiError{status: http.StatusNotImplemented, code: "NOT_IMPLEMENTED",
		message: "Password reset email is not configured on this server"}
}

// bootstrapAdmin creates the admin account on first run, or replaces its
// password when reset is true. It returns the password that was set.
func (a *app) bootstrapAdmin(password string, reset bool) (bool, error) {
	ctx := context.Background()
	var count int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 && !reset {
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	now := nowStamp()
	var exists int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ?`, adminEmail).Scan(&exists); err != nil {
		return false, err
	}
	if exists > 0 {
		_, err = a.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE email = ?`,
			string(hash), now, adminEmail)
		return false, err
	}
	_, err = a.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, first_name, last_name, role, created_at, updated_at)
		 VALUES (?, ?, ?, 'Admin', '', 'admin', ?, ?)`,
		newID(), adminEmail, string(hash), now, now)
	return true, err
}

func generatePassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
