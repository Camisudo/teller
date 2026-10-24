package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

type userInput struct {
	Email     *string `json:"email"`
	FirstName *string `json:"firstName"`
	LastName  *string `json:"lastName"`
	Role      *string `json:"role"`
	Active    *bool   `json:"active"`
	ManagerID *string `json:"managerId"`
	Password  *string `json:"password"`
}

// listUsers returns every account. Admin only.
func (a *app) listUsers(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 20, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}
	if s := strings.TrimSpace(q.Get("search")); s != "" {
		like := "%" + s + "%"
		where = append(where, "(email LIKE ? OR first_name LIKE ? OR last_name LIKE ?)")
		args = append(args, like, like, like)
	}
	if role := q.Get("role"); role != "" {
		if err := validateEnum([]string{role}, allRoles, "role"); err != nil {
			return err
		}
		where = append(where, "role = ?")
		args = append(args, role)
	}
	if st := q.Get("status"); st != "" {
		switch st {
		case "active":
			where = append(where, "active = 1")
		case "inactive":
			where = append(where, "active = 0")
		default:
			return errBadRequest("unsupported status: " + st)
		}
	}
	order, err := orderClause(r, map[string]string{
		"createdAt": "created_at", "email": "email", "firstName": "first_name", "lastName": "last_name",
	}, "createdAt", "desc", "id")
	if err != nil {
		return err
	}
	items, total, err := queryPage(r.Context(), a.db, userSelect, "FROM users",
		" WHERE "+strings.Join(where, " AND "), order, args, p, scanUser)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

// createUser adds an account with a chosen password. Admin only.
func (a *app) createUser(w http.ResponseWriter, r *http.Request) error {
	var in userInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	email := ""
	if in.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*in.Email))
	}
	if !emailRe.MatchString(email) {
		return errValidation("email", "Invalid email format")
	}
	first := ""
	if in.FirstName != nil {
		first = strings.TrimSpace(*in.FirstName)
	}
	if first == "" {
		return errValidation("firstName", "First name is required")
	}
	last := ""
	if in.LastName != nil {
		last = strings.TrimSpace(*in.LastName)
	}
	role := roleSalesRep
	if in.Role != nil {
		role = *in.Role
	}
	if !contains(allRoles, role) {
		return errValidation("role", "Role must be admin, sales_manager, sales_rep, or customer")
	}
	if in.Password == nil || len(*in.Password) < minPasswordLen {
		return errValidation("password", "Password must be at least 10 characters")
	}
	ctx := r.Context()
	var n int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = ?`, email).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errConflict("A user with that email already exists")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	id := newID()
	now := nowStamp()
	var mgr any
	if in.ManagerID != nil && *in.ManagerID != "" {
		if err := a.checkManager(ctx, id, *in.ManagerID); err != nil {
			return err
		}
		mgr = *in.ManagerID
	}
	_, err = a.db.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, first_name, last_name, role, active, manager_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
		id, email, string(hash), first, last, role, mgr, now, now)
	if err != nil {
		return err
	}
	u, err := a.loadUser(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, u)
	return nil
}

// updateUser changes name, role, active flag, or password. Admin only.
// Guards: an admin cannot deactivate or demote themselves, and the last
// active admin cannot be deactivated or demoted.
func (a *app) updateUser(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	var in userInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	current, err := a.loadUser(ctx, id)
	if err != nil {
		return err
	}

	if in.Role != nil {
		if !contains(allRoles, *in.Role) {
			return errValidation("role", "Role must be admin, sales_manager, sales_rep, or customer")
		}
	}
	if in.Password != nil && len(*in.Password) < minPasswordLen {
		return errValidation("password", "Password must be at least 10 characters")
	}
	// Email is the login identifier and is not editable here.
	if in.Email != nil && strings.ToLower(strings.TrimSpace(*in.Email)) != current.Email {
		return errValidation("email", "Email cannot be changed")
	}
	if in.FirstName != nil && strings.TrimSpace(*in.FirstName) == "" {
		return errValidation("firstName", "First name is required")
	}
	if in.ManagerID != nil && *in.ManagerID != "" {
		if err := a.checkManager(ctx, id, *in.ManagerID); err != nil {
			return err
		}
	}

	self := currentUserID(r) == id
	newRole := current.Role
	if in.Role != nil {
		newRole = *in.Role
	}
	newActive := current.Active
	if in.Active != nil {
		newActive = *in.Active
	}
	losesAdmin := current.Role == roleAdmin && current.Active && (newRole != roleAdmin || !newActive)
	if losesAdmin {
		if self {
			return errConflict("You cannot deactivate or demote your own admin account")
		}
		if err := a.ensureAnotherActiveAdmin(ctx, id); err != nil {
			return err
		}
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sets := []string{"updated_at = ?"}
	args := []any{nowStamp()}
	if in.FirstName != nil {
		sets = append(sets, "first_name = ?")
		args = append(args, strings.TrimSpace(*in.FirstName))
	}
	if in.LastName != nil {
		sets = append(sets, "last_name = ?")
		args = append(args, strings.TrimSpace(*in.LastName))
	}
	if in.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, newRole)
	}
	if in.Active != nil {
		sets = append(sets, "active = ?")
		args = append(args, boolInt(newActive))
	}
	if in.ManagerID != nil {
		sets = append(sets, "manager_id = ?")
		args = append(args, nilIfEmpty(*in.ManagerID))
	}
	if in.Password != nil {
		hash, err := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		sets = append(sets, "password_hash = ?")
		args = append(args, string(hash))
	}
	args = append(args, id)
	if _, err := tx.ExecContext(ctx, "UPDATE users SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	u, err := a.loadUser(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, u)
	return nil
}

// checkManager validates a proposed manager for user id: the manager must exist
// and be active, cannot be the user, and cannot sit below the user in the
// reporting tree (that would form a cycle).
func (a *app) checkManager(ctx context.Context, id, managerID string) error {
	if managerID == id {
		return errValidation("managerId", "A user cannot manage themselves")
	}
	var active int
	err := a.db.QueryRowContext(ctx, `SELECT active FROM users WHERE id = ?`, managerID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return errValidation("managerId", "Manager not found")
	}
	if err != nil {
		return err
	}
	if active == 0 {
		return errValidation("managerId", "Manager is deactivated")
	}
	below, err := a.subtree(ctx, id)
	if err != nil {
		return err
	}
	if contains(below, managerID) {
		return errValidation("managerId", "That would create a reporting loop")
	}
	return nil
}

// ensureAnotherActiveAdmin fails unless some active admin other than id exists.
func (a *app) ensureAnotherActiveAdmin(ctx context.Context, id string) error {
	var n int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND active = 1 AND id <> ?`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		n = 0
	} else if err != nil {
		return err
	}
	if n == 0 {
		return errConflict("At least one active admin must remain")
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
