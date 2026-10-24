package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

var dealStages = []string{"prospect", "qualified", "proposal", "negotiation", "closed_won", "closed_lost"}

func isClosedStage(s string) bool { return s == "closed_won" || s == "closed_lost" }

type dealRef struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Stage string  `json:"stage,omitempty"`
	Value float64 `json:"value,omitempty"`
}

type dealOut struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	Description       string         `json:"description"`
	Value             float64        `json:"value"`
	Currency          string         `json:"currency"`
	Stage             string         `json:"stage"`
	Probability       int            `json:"probability"`
	Contact           *contactRef    `json:"contact"`
	Owner             userRef        `json:"owner"`
	ExpectedCloseDate *string        `json:"expectedCloseDate"`
	ClosedDate        *string        `json:"closedDate"`
	ClosedReason      *string        `json:"closedReason"`
	Tags              []string       `json:"tags"`
	CustomFields      map[string]any `json:"customFields"`
	CreatedAt         string         `json:"createdAt"`
	UpdatedAt         string         `json:"updatedAt"`
}

type dealInput struct {
	Title             *string         `json:"title"`
	Description       *string         `json:"description"`
	Value             *float64        `json:"value"`
	Currency          *string         `json:"currency"`
	Stage             *string         `json:"stage"`
	Probability       *int            `json:"probability"`
	ContactID         *string         `json:"contactId"`
	ExpectedCloseDate *string         `json:"expectedCloseDate"`
	Tags              *[]string       `json:"tags"`
	CustomFields      *map[string]any `json:"customFields"`
}

type dealDetail struct {
	dealOut
	Tasks      []taskBrief   `json:"tasks"`
	Notes      []noteOut     `json:"notes"`
	Activities []activityOut `json:"activities"`
}

const (
	dealCols = `SELECT d.id, d.title, d.description, d.value, d.currency, d.stage, d.probability,
		d.expected_close_date, d.closed_date, d.closed_reason, d.tags, d.custom_fields, d.created_at, d.updated_at,
		c.id, c.first_name, c.last_name, u.id, u.first_name, u.last_name`
	dealFrom = `FROM deals d
		JOIN users u ON u.id = d.owner_id
		LEFT JOIN contacts c ON c.id = d.contact_id AND c.deleted_at IS NULL`
)

func scanDeal(s rowScanner) (dealOut, error) {
	var d dealOut
	var expected, closed, reason sql.NullString
	var cid, cfirst, clast sql.NullString
	var tags, custom string
	err := s.Scan(&d.ID, &d.Title, &d.Description, &d.Value, &d.Currency, &d.Stage, &d.Probability,
		&expected, &closed, &reason, &tags, &custom, &d.CreatedAt, &d.UpdatedAt,
		&cid, &cfirst, &clast, &d.Owner.ID, &d.Owner.FirstName, &d.Owner.LastName)
	if err != nil {
		return d, err
	}
	d.ExpectedCloseDate = strPtr(expected)
	d.ClosedDate = strPtr(closed)
	d.ClosedReason = strPtr(reason)
	if cid.Valid {
		d.Contact = &contactRef{ID: cid.String, FirstName: cfirst.String, LastName: clast.String}
	}
	d.Tags = []string{}
	d.CustomFields = map[string]any{}
	_ = json.Unmarshal([]byte(tags), &d.Tags)
	_ = json.Unmarshal([]byte(custom), &d.CustomFields)
	return d, nil
}

func (a *app) getDeal(ctx context.Context, id string) (dealOut, error) {
	row := a.db.QueryRowContext(ctx, dealCols+" "+dealFrom+" WHERE d.id = ? AND d.deleted_at IS NULL", id)
	d, err := scanDeal(row)
	if errors.Is(err, sql.ErrNoRows) {
		return d, errNotFound("Deal")
	}
	return d, err
}

// dealBriefs lists deals with a WHERE clause on the deals table alias "d".
func (a *app) dealBriefs(ctx context.Context, where string, arg any) ([]dealRef, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT d.id, d.title, d.stage, d.value FROM deals d WHERE `+where+` AND d.deleted_at IS NULL
		 ORDER BY d.created_at DESC LIMIT 50`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dealRef{}
	for rows.Next() {
		var d dealRef
		if err := rows.Scan(&d.ID, &d.Title, &d.Stage, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func applyDeal(d *dealOut, in dealInput) {
	if in.Title != nil {
		d.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		d.Description = *in.Description
	}
	if in.Value != nil {
		d.Value = *in.Value
	}
	if in.Currency != nil {
		d.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
	}
	if in.Stage != nil {
		d.Stage = *in.Stage
	}
	if in.Probability != nil {
		d.Probability = *in.Probability
	}
	if in.ExpectedCloseDate != nil {
		if *in.ExpectedCloseDate == "" {
			d.ExpectedCloseDate = nil
		} else {
			v := *in.ExpectedCloseDate
			d.ExpectedCloseDate = &v
		}
	}
	if in.Tags != nil {
		d.Tags = *in.Tags
	}
	if in.CustomFields != nil {
		d.CustomFields = *in.CustomFields
	}
}

func validateDeal(d dealOut) error {
	if d.Title == "" {
		return errValidation("title", "Title is required")
	}
	if d.Value < 0 {
		return errValidation("value", "Value cannot be negative")
	}
	if d.Probability < 0 || d.Probability > 100 {
		return errValidation("probability", "Probability must be between 0 and 100")
	}
	if !contains(dealStages, d.Stage) {
		return errValidation("stage", "Unknown stage")
	}
	if d.Currency == "" {
		return errValidation("currency", "Currency is required")
	}
	return nil
}

// applyClosure keeps closedDate/closedReason in step with the stage.
func applyClosure(d *dealOut, prevStage, now string) {
	if !isClosedStage(d.Stage) {
		d.ClosedDate = nil
		d.ClosedReason = nil
		return
	}
	if prevStage != d.Stage || d.ClosedDate == nil {
		s := now
		d.ClosedDate = &s
	}
	reason := "lost"
	if d.Stage == "closed_won" {
		reason = "won"
	}
	d.ClosedReason = &reason
}

var dealSortCols = map[string]string{
	"createdAt":         "d.created_at",
	"value":             "d.value",
	"expectedCloseDate": "d.expected_close_date",
}

func (a *app) listDeals(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 20, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"d.deleted_at IS NULL"}
	args := []any{}

	if s := strings.TrimSpace(q.Get("search")); s != "" {
		where = append(where, "d.title LIKE ?")
		args = append(args, "%"+s+"%")
	}
	if stages := csvParam(r, "stage"); len(stages) > 0 {
		if err := validateEnum(stages, dealStages, "stage"); err != nil {
			return err
		}
		where = append(where, "d.stage IN ("+placeholders(len(stages))+")")
		args = append(args, toAny(stages)...)
	}
	if owner := q.Get("owner"); owner != "" {
		where = append(where, "d.owner_id = ?")
		args = append(args, owner)
	}
	for _, f := range []struct{ key, op string }{{"minValue", ">="}, {"maxValue", "<="}} {
		if v := q.Get(f.key); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return errBadRequest(f.key + " must be a number")
			}
			where = append(where, "d.value "+f.op+" ?")
			args = append(args, n)
		}
	}

	order, err := orderClause(r, dealSortCols, "createdAt", "desc", "d.id")
	if err != nil {
		return err
	}
	items, total, err := queryPage(r.Context(), a.db, dealCols, dealFrom,
		" WHERE "+strings.Join(where, " AND "), order, args, p, scanDeal)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

func (a *app) createDeal(w http.ResponseWriter, r *http.Request) error {
	var in dealInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	d := dealOut{Currency: "USD", Stage: "prospect", Probability: 50, Tags: []string{}, CustomFields: map[string]any{}}
	applyDeal(&d, in)
	if err := validateDeal(d); err != nil {
		return err
	}
	contactID := ""
	if in.ContactID != nil && *in.ContactID != "" {
		ok, err := a.contactExists(ctx, *in.ContactID)
		if err != nil {
			return err
		}
		if !ok {
			return errValidation("contactId", "Contact not found")
		}
		contactID = *in.ContactID
	}

	actor := currentUserID(r)
	id := newID()
	now := nowStamp()
	applyClosure(&d, "", now)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO deals (id, title, description, value, currency, stage, probability, contact_id, owner_id,
			expected_close_date, closed_date, closed_reason, tags, custom_fields, created_at, updated_at)
		 VALUES (`+placeholders(16)+`)`,
		id, d.Title, d.Description, d.Value, d.Currency, d.Stage, d.Probability, nilIfEmpty(contactID), actor,
		strOrNil(d.ExpectedCloseDate), strOrNil(d.ClosedDate), strOrNil(d.ClosedReason),
		jsonText(d.Tags, "[]"), jsonText(d.CustomFields, "{}"), now, now)
	if err != nil {
		return err
	}
	if err := logActivity(ctx, tx, actor, "deal_created", "Deal created: "+d.Title,
		activityRefs{dealID: id, contactID: contactID}, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getDeal(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

func (a *app) updateDeal(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	prev, err := a.getDeal(ctx, id)
	if err != nil {
		return err
	}
	var in dealInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	cur := prev
	applyDeal(&cur, in)
	if err := validateDeal(cur); err != nil {
		return err
	}

	contactID := ""
	if cur.Contact != nil {
		contactID = cur.Contact.ID
	}
	if in.ContactID != nil {
		contactID = *in.ContactID
		if contactID != "" {
			ok, err := a.contactExists(ctx, contactID)
			if err != nil {
				return err
			}
			if !ok {
				return errValidation("contactId", "Contact not found")
			}
		}
	}

	actor := currentUserID(r)
	now := nowStamp()
	applyClosure(&cur, prev.Stage, now)

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`UPDATE deals SET title = ?, description = ?, value = ?, currency = ?, stage = ?, probability = ?,
			contact_id = ?, expected_close_date = ?, closed_date = ?, closed_reason = ?, tags = ?,
			custom_fields = ?, updated_at = ? WHERE id = ?`,
		cur.Title, cur.Description, cur.Value, cur.Currency, cur.Stage, cur.Probability,
		nilIfEmpty(contactID), strOrNil(cur.ExpectedCloseDate), strOrNil(cur.ClosedDate), strOrNil(cur.ClosedReason),
		jsonText(cur.Tags, "[]"), jsonText(cur.CustomFields, "{}"), now, id)
	if err != nil {
		return err
	}

	refs := activityRefs{dealID: id, contactID: contactID}
	if prev.Stage != cur.Stage {
		if err := logActivity(ctx, tx, actor, "deal_stage_changed",
			fmt.Sprintf("Deal moved from %s to %s", prev.Stage, cur.Stage), refs,
			map[string]any{"from": prev.Stage, "to": cur.Stage}); err != nil {
			return err
		}
	}
	if prev.Value != cur.Value {
		if err := logActivity(ctx, tx, actor, "deal_value_changed",
			fmt.Sprintf("Deal value changed from %.2f to %.2f", prev.Value, cur.Value), refs,
			map[string]any{"from": prev.Value, "to": cur.Value}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getDeal(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *app) deleteDeal(w http.ResponseWriter, r *http.Request) error {
	now := nowStamp()
	res, err := a.db.ExecContext(r.Context(),
		`UPDATE deals SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now, now, r.PathValue("id"))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Deal")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *app) getDealDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	d, err := a.getDeal(ctx, id)
	if err != nil {
		return err
	}
	tasks, err := a.taskBriefs(ctx, "deal_id", id)
	if err != nil {
		return err
	}
	notes, _, err := a.listNotes(ctx, " WHERE n.deal_id = ?", []any{id}, " ORDER BY n.created_at DESC, n.rowid DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	acts, _, err := a.listActivities(ctx, " WHERE a.deal_id = ?", []any{id}, " ORDER BY a.created_at DESC, a.rowid DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, dealDetail{dealOut: d, Tasks: tasks, Notes: notes, Activities: acts})
	return nil
}
