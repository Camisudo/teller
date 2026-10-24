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

const minPasswordLen = 10

const (
	roleAdmin        = "admin"
	roleSalesManager = "sales_manager"
	roleSalesRep     = "sales_rep"
	roleCustomer     = "customer"
)

var allRoles = []string{roleAdmin, roleSalesManager, roleSalesRep, roleCustomer}

type ctxUserKey struct{}

// authUser is the caller resolved from the database on every request, so a
// role change or deactivation applies immediately, not at token expiry.
type authUser struct {
	ID   string
	Role string
	// Scope lists the user ids whose records this caller may see. nil means
	// unrestricted (admin). Filled in by buildScope on every request.
	Scope []string
}

func ctxAuthUser(ctx context.Context) *authUser {
	u, _ := ctx.Value(ctxUserKey{}).(*authUser)
	return u
}

type userRef struct {
	ID        string `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type userOut struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	FirstName   string  `json:"firstName"`
	LastName    string  `json:"lastName"`
	Role        string  `json:"role"`
	Avatar      string  `json:"avatar"`
	Active      bool    `json:"active"`
	ManagerID   *string `json:"managerId"`
	Permissions []any   `json:"permissions"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
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

// resolveUser loads the caller's current role and active flag. Deactivated
// users are rejected even when their token is still within its TTL.
func (a *app) resolveUser(ctx context.Context, id string) (*authUser, error) {
	var u authUser
	var active int
	err := a.db.QueryRowContext(ctx, `SELECT id, role, active FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Role, &active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && active == 0) {
		return nil, errUnauthorized("Invalid or expired token")
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (a *app) protect(h handlerFunc) http.HandlerFunc {
	return a.guard(nil, h)
}

// requireRole is protect plus a role check. The check happens before the
// handler runs, so no handler can forget it.
func (a *app) requireRole(roles []string, h handlerFunc) http.HandlerFunc {
	return a.guard(roles, h)
}

func (a *app) guard(roles []string, h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid, err := a.authenticate(r)
		if err == nil {
			var u *authUser
			u, err = a.resolveUser(r.Context(), uid)
			if err == nil {
				if roles != nil && !contains(roles, u.Role) {
					err = errForbidden("You do not have permission to perform this action")
				} else {
					err = a.buildScope(r.Context(), u)
					r = r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, u))
				}
			}
		}
		if err != nil {
			writeError(w, err)
			return
		}
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

func currentUser(r *http.Request) *authUser {
	u, _ := r.Context().Value(ctxUserKey{}).(*authUser)
	return u
}

func currentUserID(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return u.ID
	}
	return ""
}

const userSelect = `SELECT id, email, first_name, last_name, role, avatar, active, manager_id, created_at, updated_at`
const userCols = userSelect + ` FROM users`

func scanUser(s rowScanner) (userOut, error) {
	var u userOut
	var active int
	var manager sql.NullString
	err := s.Scan(&u.ID, &u.Email, &u.FirstName, &u.LastName, &u.Role, &u.Avatar, &active, &manager, &u.CreatedAt, &u.UpdatedAt)
	u.Active = active != 0
	u.ManagerID = strPtr(manager)
	u.Permissions = []any{}
	return u, err
}

func (a *app) loadUser(ctx context.Context, id string) (userOut, error) {
	u, err := scanUser(a.db.QueryRowContext(ctx, userCols+" WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound("User")
	}
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
	var active int
	err := a.db.QueryRowContext(r.Context(),
		`SELECT id, password_hash, active FROM users WHERE email = ?`, email).
		Scan(&id, &hash, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return errUnauthorized("Invalid credentials")
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		return errUnauthorized("Invalid credentials")
	}
	if active == 0 {
		return errUnauthorized("This account is deactivated")
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
		_, err = a.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, active = 1, updated_at = ? WHERE email = ?`,
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
