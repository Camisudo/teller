package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// activityRefs names the records an activity relates to. Empty ids become NULL.
type activityRefs struct {
	contactID string
	dealID    string
	taskID    string
}

type activityOut struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Actor       userRef        `json:"actor"`
	RelatedTo   relatedTo      `json:"relatedTo"`
	Metadata    map[string]any `json:"metadata"`
	CreatedAt   string         `json:"createdAt"`
}

// execContexter is the subset shared by *sql.Tx and *sql.DB used for writes.
type execContexter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// logActivity writes an audit entry in the same transaction as the change it describes.
func logActivity(ctx context.Context, tx execContexter, actor, typ, description string,
	refs activityRefs, metadata map[string]any) error {
	meta := metadata
	if meta == nil {
		meta = map[string]any{}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO activities (id, type, description, actor_id, contact_id, deal_id, task_id, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), typ, description, actor, nilIfEmpty(refs.contactID), nilIfEmpty(refs.dealID),
		nilIfEmpty(refs.taskID), jsonText(meta, "{}"), nowStamp())
	return err
}

const activityCols = `SELECT a.id, a.type, a.description, a.metadata, a.created_at,
	a.contact_id, a.deal_id, a.task_id,
	u.id, u.first_name, u.last_name,
	c.id, c.first_name, c.last_name,
	d.id, d.title,
	t.id, t.title`

const activityFrom = `FROM activities a
	JOIN users u ON u.id = a.actor_id
	LEFT JOIN contacts c ON c.id = a.contact_id AND c.deleted_at IS NULL
	LEFT JOIN deals d ON d.id = a.deal_id AND d.deleted_at IS NULL
	LEFT JOIN tasks t ON t.id = a.task_id`

func scanActivity(s rowScanner) (activityOut, error) {
	var a activityOut
	var meta string
	var acid, adid, atid sql.NullString
	var ccid, cfirst, clast, did, dtitle, tid, ttitle sql.NullString
	err := s.Scan(&a.ID, &a.Type, &a.Description, &meta, &a.CreatedAt,
		&acid, &adid, &atid,
		&a.Actor.ID, &a.Actor.FirstName, &a.Actor.LastName,
		&ccid, &cfirst, &clast, &did, &dtitle, &tid, &ttitle)
	if err != nil {
		return a, err
	}
	a.Metadata = map[string]any{}
	_ = json.Unmarshal([]byte(meta), &a.Metadata)
	if ccid.Valid {
		a.RelatedTo.Contact = &contactRef{ID: ccid.String, FirstName: cfirst.String, LastName: clast.String}
	}
	if did.Valid {
		a.RelatedTo.Deal = &dealRef{ID: did.String, Title: dtitle.String}
	}
	return a, nil
}

// listActivities returns one page. Callers pass fixed SQL fragments and bound args.
func (a *app) listActivities(ctx context.Context, where string, args []any, order string, p pageParams) ([]activityOut, int, error) {
	var total int
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) "+activityFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qargs := append(append([]any{}, args...), p.limit, p.offset())
	rows, err := a.db.QueryContext(ctx, activityCols+" "+activityFrom+where+order+" LIMIT ? OFFSET ?", qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []activityOut{}
	for rows.Next() {
		item, err := scanActivity(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

var activitySortCols = map[string]string{"createdAt": "a.created_at"}

func (a *app) listActivitiesHandler(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 50, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}

	if types := csvParam(r, "type"); len(types) > 0 {
		where = append(where, "a.type IN ("+placeholders(len(types))+")")
		args = append(args, toAny(types)...)
	}
	if actor := q.Get("actor"); actor != "" {
		where = append(where, "a.actor_id = ?")
		args = append(args, actor)
	}
	switch rt := q.Get("relatedToType"); rt {
	case "":
	case "contact":
		where = append(where, "a.contact_id IS NOT NULL")
	case "deal":
		where = append(where, "a.deal_id IS NOT NULL")
	case "task":
		where = append(where, "a.task_id IS NOT NULL")
	default:
		return errBadRequest("relatedToType must be contact, deal or task")
	}
	if id := q.Get("relatedToId"); id != "" {
		where = append(where, "(a.contact_id = ? OR a.deal_id = ? OR a.task_id = ?)")
		args = append(args, id, id, id)
	}
	if from := q.Get("fromDate"); from != "" {
		where = append(where, "date(a.created_at) >= date(?)")
		args = append(args, from)
	}
	if to := q.Get("toDate"); to != "" {
		where = append(where, "date(a.created_at) <= date(?)")
		args = append(args, to)
	}

	order, err := orderClause(r, activitySortCols, "createdAt", "desc", "a.rowid")
	if err != nil {
		return err
	}
	items, total, err := a.listActivities(r.Context(), " WHERE "+strings.Join(where, " AND "), args, order, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

func (a *app) getActivityHandler(w http.ResponseWriter, r *http.Request) error {
	row := a.db.QueryRowContext(r.Context(), activityCols+" "+activityFrom+" WHERE a.id = ?", r.PathValue("id"))
	item, err := scanActivity(row)
	if errors.Is(err, sql.ErrNoRows) {
		return errNotFound("Activity")
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, item)
	return nil
}

// ---- notes ----

type noteOut struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Author    userRef   `json:"author"`
	RelatedTo relatedTo `json:"relatedTo"`
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

const noteCols = `SELECT n.id, n.content, n.created_at, n.updated_at,
	u.id, u.first_name, u.last_name,
	c.id, c.first_name, c.last_name, d.id, d.title, t.id, t.title`
const noteFrom = `FROM notes n
	JOIN users u ON u.id = n.author_id
	LEFT JOIN contacts c ON c.id = n.contact_id AND c.deleted_at IS NULL
	LEFT JOIN deals d ON d.id = n.deal_id AND d.deleted_at IS NULL
	LEFT JOIN tasks t ON t.id = n.task_id`

func scanNote(s rowScanner) (noteOut, error) {
	var n noteOut
	var ccid, cfirst, clast, did, dtitle, tid, ttitle sql.NullString
	err := s.Scan(&n.ID, &n.Content, &n.CreatedAt, &n.UpdatedAt,
		&n.Author.ID, &n.Author.FirstName, &n.Author.LastName,
		&ccid, &cfirst, &clast, &did, &dtitle, &tid, &ttitle)
	if err != nil {
		return n, err
	}
	if ccid.Valid {
		n.RelatedTo.Contact = &contactRef{ID: ccid.String, FirstName: cfirst.String, LastName: clast.String}
	}
	if did.Valid {
		n.RelatedTo.Deal = &dealRef{ID: did.String, Title: dtitle.String}
	}
	if tid.Valid {
		n.RelatedTo.Task = &taskRef{ID: tid.String, Title: ttitle.String}
	}
	return n, nil
}

func (a *app) listNotes(ctx context.Context, where string, args []any, order string, p pageParams) ([]noteOut, int, error) {
	var total int
	if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) "+noteFrom+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qargs := append(append([]any{}, args...), p.limit, p.offset())
	rows, err := a.db.QueryContext(ctx, noteCols+" "+noteFrom+where+order+" LIMIT ? OFFSET ?", qargs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []noteOut{}
	for rows.Next() {
		item, err := scanNote(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (a *app) listContactNotes(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 20, 100)
	if err != nil {
		return err
	}
	cid := r.PathValue("contactId")
	ok, err := a.contactExists(r.Context(), cid)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("Contact")
	}
	order := " ORDER BY n.created_at DESC, n.rowid DESC"
	if strings.ToLower(r.URL.Query().Get("sortOrder")) == "asc" {
		order = " ORDER BY n.created_at ASC, n.rowid ASC"
	}
	items, total, err := a.listNotes(r.Context(), " WHERE n.contact_id = ?", []any{cid}, order, p)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

func (a *app) createContactNote(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	cid := r.PathValue("contactId")
	var in struct {
		Content string `json:"content"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return errValidation("content", "Note content is required")
	}
	ok, err := a.contactExists(ctx, cid)
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("Contact")
	}

	actor := currentUserID(r)
	id := newID()
	now := nowStamp()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO notes (id, content, author_id, contact_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, content, actor, cid, now, now); err != nil {
		return err
	}
	if err := logActivity(ctx, tx, actor, "note_added", "Note added", activityRefs{contactID: cid}, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	n, err := a.getNote(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, n)
	return nil
}

func (a *app) getNote(ctx context.Context, id string) (noteOut, error) {
	n, err := scanNote(a.db.QueryRowContext(ctx, noteCols+" "+noteFrom+" WHERE n.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, errNotFound("Note")
	}
	return n, err
}

func (a *app) updateNote(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	var in struct {
		Content *string `json:"content"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	if in.Content == nil || strings.TrimSpace(*in.Content) == "" {
		return errValidation("content", "Note content is required")
	}
	res, err := a.db.ExecContext(ctx, `UPDATE notes SET content = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(*in.Content), nowStamp(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Note")
	}
	n, err := a.getNote(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, n)
	return nil
}

func (a *app) deleteNote(w http.ResponseWriter, r *http.Request) error {
	res, err := a.db.ExecContext(r.Context(), `DELETE FROM notes WHERE id = ?`, r.PathValue("id"))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Note")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
