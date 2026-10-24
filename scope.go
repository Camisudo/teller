package main

import (
	"context"
	"fmt"
)

// Record visibility rules, applied to every read and write:
//   admin          sees everything
//   sales_manager  sees records owned by themselves and anyone in their reporting tree
//   sales_rep      sees only records they own (contacts, deals) or are assigned (tasks)
//   customer       same as sales_rep
// Contacts and deals are scoped by owner_id. Tasks are scoped by assigned_to_id.
// Activities are scoped by actor_id. Notes are scoped by author_id.

// buildScope fills u.Scope with the user ids whose records u may see.
// nil means unrestricted (admin). Non-admins always get a non-nil slice.
func (a *app) buildScope(ctx context.Context, u *authUser) error {
	switch u.Role {
	case roleAdmin:
		u.Scope = nil
		return nil
	case roleSalesManager:
		ids, err := a.subtree(ctx, u.ID)
		if err != nil {
			return err
		}
		u.Scope = ids
		return nil
	default:
		u.Scope = []string{u.ID}
		return nil
	}
}

// subtree returns rootID plus every user that reports to it, directly or
// through a chain of managers. UNION (not UNION ALL) keeps it finite even
// if bad data ever formed a cycle.
func (a *app) subtree(ctx context.Context, rootID string) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, `
		WITH RECURSIVE tree(id) AS (
			SELECT id FROM users WHERE id = ?
			UNION
			SELECT u.id FROM users u JOIN tree t ON u.manager_id = t.id
		)
		SELECT id FROM tree`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// visibleIn returns a predicate restricting column to the caller's scope,
// or "" when the caller is unrestricted. With no user in context it fails closed.
func visibleIn(ctx context.Context, column string) (string, []any) {
	u := ctxAuthUser(ctx)
	if u == nil {
		return "1=0", nil
	}
	if u.Scope == nil {
		return "", nil
	}
	return fmt.Sprintf("%s IN (%s)", column, placeholders(len(u.Scope))), toAny(u.Scope)
}

// andVisible is visibleIn in the form " AND <predicate>", for raw SQL that
// already has a WHERE clause. Returns "" when no restriction applies.
func andVisible(ctx context.Context, column string) (string, []any) {
	frag, args := visibleIn(ctx, column)
	if frag == "" {
		return "", nil
	}
	return " AND " + frag, args
}

// addVisible appends the scope predicate to a where-list and its args together,
// keeping placeholders and arguments in the same order.
func addVisible(ctx context.Context, where []string, args []any, column string) ([]string, []any) {
	frag, fa := visibleIn(ctx, column)
	if frag == "" {
		return where, args
	}
	return append(where, frag), append(args, fa...)
}

// scopeSQL is andVisible for call sites that build SQL without a context-free helper.
func scopeSQL(ctx context.Context, column string) string {
	frag, _ := andVisible(ctx, column)
	return frag
}

// scopeArgs returns the bind arguments matching scopeSQL.
func scopeArgs(ctx context.Context) []any {
	_, args := andVisible(ctx, "")
	return args
}

// inScope reports whether the caller may act on records owned by or assigned to id.
func inScope(ctx context.Context, id string) bool {
	u := ctxAuthUser(ctx)
	if u == nil {
		return false
	}
	if u.Scope == nil {
		return true
	}
	return contains(u.Scope, id)
}
