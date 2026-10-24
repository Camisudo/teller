package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

type apiError struct {
	status  int
	code    string
	message string
	details map[string]any
}

func (e *apiError) Error() string { return e.message }

func errBadRequest(msg string) error {
	return &apiError{status: http.StatusBadRequest, code: "BAD_REQUEST", message: msg}
}

func errValidation(field, msg string) error {
	return &apiError{
		status:  http.StatusUnprocessableEntity,
		code:    "VALIDATION_ERROR",
		message: msg,
		details: map[string]any{"field": field},
	}
}

func errNotFound(what string) error {
	return &apiError{status: http.StatusNotFound, code: "NOT_FOUND", message: what + " not found"}
}

func errUnauthorized(msg string) error {
	return &apiError{status: http.StatusUnauthorized, code: "UNAUTHORIZED", message: msg}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		log.Printf("internal error: %v", err)
		ae = &apiError{status: http.StatusInternalServerError, code: "INTERNAL_ERROR", message: "Internal server error"}
	}
	e := map[string]any{"code": ae.code, "message": ae.message}
	if ae.details != nil {
		e["details"] = ae.details
	}
	writeJSON(w, ae.status, map[string]any{"error": e})
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return errBadRequest("invalid JSON body")
	}
	return nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type pageParams struct{ page, limit int }

func (p pageParams) offset() int { return (p.page - 1) * p.limit }

type pagination struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"totalPages"`
}

func parsePaging(r *http.Request, defaultLimit, maxLimit int) (pageParams, error) {
	p := pageParams{page: 1, limit: defaultLimit}
	q := r.URL.Query()
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return p, errBadRequest("page must be a positive integer")
		}
		p.page = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			return p, errBadRequest(fmt.Sprintf("limit must be between 1 and %d", maxLimit))
		}
		p.limit = n
	}
	return p, nil
}

func newPagination(p pageParams, total int) pagination {
	pages := 0
	if total > 0 {
		pages = (total + p.limit - 1) / p.limit
	}
	return pagination{Page: p.page, Limit: p.limit, Total: total, TotalPages: pages}
}

// csvParam reads a comma-separated query parameter, as the API spec requires.
func csvParam(r *http.Request, key string) []string {
	var out []string
	for _, part := range strings.Split(r.URL.Query().Get(key), ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func validateEnum(values []string, allowed []string, field string) error {
	for _, v := range values {
		if !contains(allowed, v) {
			return errBadRequest(fmt.Sprintf("unsupported %s: %s", field, v))
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toAny[T any](in []T) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// orderClause builds an ORDER BY from a whitelist. Column expressions come
// only from the allowed map, so user input never reaches the SQL text.
func orderClause(r *http.Request, allowed map[string]string, defSort, defDir, tiebreak string) (string, error) {
	sortBy := r.URL.Query().Get("sortBy")
	if sortBy == "" {
		sortBy = defSort
	}
	col, ok := allowed[sortBy]
	if !ok {
		return "", errBadRequest("unsupported sortBy: " + sortBy)
	}
	dir := strings.ToLower(r.URL.Query().Get("sortOrder"))
	if dir == "" {
		dir = defDir
	}
	if dir != "asc" && dir != "desc" {
		return "", errBadRequest("sortOrder must be asc or desc")
	}
	return " ORDER BY " + col + " " + dir + ", " + tiebreak + " " + dir, nil
}

// queryPage runs a COUNT and a paged SELECT with the same FROM/WHERE.
func queryPage[T any](ctx context.Context, db *sql.DB, selectCols, fromSQL, where, order string,
	args []any, p pageParams, scan func(rowScanner) (T, error)) ([]T, int, error) {
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) "+fromSQL+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qargs := append([]any{}, args...)
	qargs = append(qargs, p.limit, p.offset())
	rows, err := db.QueryContext(ctx, selectCols+" "+fromSQL+where+order+" LIMIT ? OFFSET ?", qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

func todayStamp() string { return time.Now().UTC().Format("2006-01-02") }

// nilIfEmpty returns nil for "" so optional foreign keys are stored as NULL.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func strOrNil(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

func jsonText(v any, empty string) string {
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return empty
	}
	return string(b)
}

func displayName(first, last, company string) string {
	name := strings.TrimSpace(first + " " + last)
	if name == "" {
		return company
	}
	return name
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
