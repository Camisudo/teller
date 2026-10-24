package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type addressOut struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
}

type contactOut struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	FirstName    string         `json:"firstName"`
	LastName     string         `json:"lastName"`
	CompanyName  string         `json:"companyName"`
	Email        string         `json:"email"`
	Phone        string         `json:"phone"`
	Website      string         `json:"website"`
	Address      addressOut     `json:"address"`
	Tags         []string       `json:"tags"`
	CustomFields map[string]any `json:"customFields"`
	Owner        userRef        `json:"owner"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`
}

// contactInput uses pointers so PATCH can tell "not sent" from "sent empty".
type contactInput struct {
	Type         *string         `json:"type"`
	FirstName    *string         `json:"firstName"`
	LastName     *string         `json:"lastName"`
	CompanyName  *string         `json:"companyName"`
	Email        *string         `json:"email"`
	Phone        *string         `json:"phone"`
	Website      *string         `json:"website"`
	Address      *addressOut     `json:"address"`
	Tags         *[]string       `json:"tags"`
	CustomFields *map[string]any `json:"customFields"`
}

type contactRef struct {
	ID        string `json:"id"`
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
}

type contactDetail struct {
	contactOut
	Deals      []dealRef     `json:"deals"`
	Tasks      []taskBrief   `json:"tasks"`
	Notes      []noteOut     `json:"notes"`
	Activities []activityOut `json:"activities"`
}

const (
	contactCols = `SELECT c.id, c.type, c.first_name, c.last_name, c.company_name, c.email, c.phone, c.website,
		c.street, c.city, c.state, c.postal_code, c.country, c.tags, c.custom_fields, c.created_at, c.updated_at,
		u.id, u.first_name, u.last_name`
	contactFrom = `FROM contacts c JOIN users u ON u.id = c.owner_id`
)

func scanContact(s rowScanner) (contactOut, error) {
	var c contactOut
	var tags, custom string
	err := s.Scan(&c.ID, &c.Type, &c.FirstName, &c.LastName, &c.CompanyName, &c.Email, &c.Phone, &c.Website,
		&c.Address.Street, &c.Address.City, &c.Address.State, &c.Address.PostalCode, &c.Address.Country,
		&tags, &custom, &c.CreatedAt, &c.UpdatedAt,
		&c.Owner.ID, &c.Owner.FirstName, &c.Owner.LastName)
	if err != nil {
		return c, err
	}
	c.Tags = []string{}
	c.CustomFields = map[string]any{}
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	_ = json.Unmarshal([]byte(custom), &c.CustomFields)
	return c, nil
}

func (a *app) getContact(ctx context.Context, id string) (contactOut, error) {
	row := a.db.QueryRowContext(ctx, contactCols+" "+contactFrom+" WHERE c.id = ? AND c.deleted_at IS NULL", id)
	c, err := scanContact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return c, errNotFound("Contact")
	}
	return c, err
}

func (a *app) contactExists(ctx context.Context, id string) (bool, error) {
	var n int
	err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE id = ? AND deleted_at IS NULL`, id).Scan(&n)
	return n > 0, err
}

func applyContact(c *contactOut, in contactInput) {
	if in.Type != nil {
		c.Type = strings.TrimSpace(*in.Type)
	}
	if in.FirstName != nil {
		c.FirstName = strings.TrimSpace(*in.FirstName)
	}
	if in.LastName != nil {
		c.LastName = strings.TrimSpace(*in.LastName)
	}
	if in.CompanyName != nil {
		c.CompanyName = strings.TrimSpace(*in.CompanyName)
	}
	if in.Email != nil {
		c.Email = strings.TrimSpace(*in.Email)
	}
	if in.Phone != nil {
		c.Phone = strings.TrimSpace(*in.Phone)
	}
	if in.Website != nil {
		c.Website = strings.TrimSpace(*in.Website)
	}
	if in.Address != nil {
		c.Address = *in.Address
	}
	if in.Tags != nil {
		c.Tags = *in.Tags
	}
	if in.CustomFields != nil {
		c.CustomFields = *in.CustomFields
	}
}

func validateContact(c contactOut) error {
	if c.Type != "individual" && c.Type != "company" {
		return errValidation("type", "Type must be individual or company")
	}
	if c.FirstName == "" {
		return errValidation("firstName", "First name is required")
	}
	if c.Email == "" || !emailRe.MatchString(c.Email) {
		return errValidation("email", "A valid email is required")
	}
	if c.Type == "individual" && c.LastName == "" {
		return errValidation("lastName", "Last name is required for individuals")
	}
	if c.Type == "company" && c.CompanyName == "" {
		return errValidation("companyName", "Company name is required for companies")
	}
	return nil
}

// ensureEmailFree enforces uniqueness among live contacts (case-insensitive).
func (a *app) ensureEmailFree(ctx context.Context, email, exceptID string) error {
	var n int
	err := a.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM contacts WHERE lower(email) = lower(?) AND deleted_at IS NULL AND id <> ?`,
		email, exceptID).Scan(&n)
	if err != nil {
		return err
	}
	if n > 0 {
		return &apiError{status: http.StatusUnprocessableEntity, code: "VALIDATION_ERROR",
			message: "Email already in use", details: map[string]any{"field": "email", "constraint": "unique"}}
	}
	return nil
}

var contactSortCols = map[string]string{
	"createdAt": "c.created_at",
	"lastName":  "c.last_name",
	"email":     "c.email",
}

func (a *app) listContacts(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 20, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"c.deleted_at IS NULL"}
	args := []any{}

	if s := strings.TrimSpace(q.Get("search")); s != "" {
		like := "%" + s + "%"
		where = append(where, "(c.first_name LIKE ? OR c.last_name LIKE ? OR c.company_name LIKE ? OR c.email LIKE ?)")
		args = append(args, like, like, like, like)
	}
	if t := q.Get("type"); t != "" {
		if err := validateEnum([]string{t}, []string{"individual", "company"}, "type"); err != nil {
			return err
		}
		where = append(where, "c.type = ?")
		args = append(args, t)
	}
	if owner := q.Get("owner"); owner != "" {
		where = append(where, "c.owner_id = ?")
		args = append(args, owner)
	}
	if tags := csvParam(r, "tags"); len(tags) > 0 {
		where = append(where, "EXISTS (SELECT 1 FROM json_each(c.tags) WHERE json_each.value IN ("+placeholders(len(tags))+"))")
		args = append(args, toAny(tags)...)
	}

	order, err := orderClause(r, contactSortCols, "createdAt", "desc", "c.id")
	if err != nil {
		return err
	}
	items, total, err := queryPage(r.Context(), a.db, contactCols, contactFrom,
		" WHERE "+strings.Join(where, " AND "), order, args, p, scanContact)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

func (a *app) createContact(w http.ResponseWriter, r *http.Request) error {
	var in contactInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	c := contactOut{Type: "individual", Tags: []string{}, CustomFields: map[string]any{}}
	applyContact(&c, in)
	if err := validateContact(c); err != nil {
		return err
	}
	ctx := r.Context()
	if err := a.ensureEmailFree(ctx, c.Email, ""); err != nil {
		return err
	}

	actor := currentUserID(r)
	id := newID()
	now := nowStamp()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO contacts (id, type, first_name, last_name, company_name, email, phone, website,
			street, city, state, postal_code, country, tags, custom_fields, owner_id, created_at, updated_at)
		 VALUES (`+placeholders(18)+`)`,
		id, c.Type, c.FirstName, c.LastName, c.CompanyName, c.Email, c.Phone, c.Website,
		c.Address.Street, c.Address.City, c.Address.State, c.Address.PostalCode, c.Address.Country,
		jsonText(c.Tags, "[]"), jsonText(c.CustomFields, "{}"), actor, now, now)
	if err != nil {
		return err
	}
	name := displayName(c.FirstName, c.LastName, c.CompanyName)
	if err := logActivity(ctx, tx, actor, "contact_created", "Contact created: "+name,
		activityRefs{contactID: id}, nil); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getContact(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

// changedContactFields reports which fields differ between the stored and
// updated contact, so the audit trail lists real changes only.
func changedContactFields(prev, cur contactOut) []string {
	var f []string
	add := func(changed bool, name string) {
		if changed {
			f = append(f, name)
		}
	}
	add(prev.Type != cur.Type, "type")
	add(prev.FirstName != cur.FirstName, "firstName")
	add(prev.LastName != cur.LastName, "lastName")
	add(prev.CompanyName != cur.CompanyName, "companyName")
	add(prev.Email != cur.Email, "email")
	add(prev.Phone != cur.Phone, "phone")
	add(prev.Website != cur.Website, "website")
	add(prev.Address != cur.Address, "address")
	add(jsonText(prev.Tags, "[]") != jsonText(cur.Tags, "[]"), "tags")
	add(jsonText(prev.CustomFields, "{}") != jsonText(cur.CustomFields, "{}"), "customFields")
	return f
}

func (a *app) updateContact(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	prev, err := a.getContact(ctx, id)
	if err != nil {
		return err
	}
	var in contactInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	cur := prev
	applyContact(&cur, in)
	if err := validateContact(cur); err != nil {
		return err
	}
	if err := a.ensureEmailFree(ctx, cur.Email, id); err != nil {
		return err
	}

	actor := currentUserID(r)
	now := nowStamp()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`UPDATE contacts SET type = ?, first_name = ?, last_name = ?, company_name = ?, email = ?, phone = ?,
			website = ?, street = ?, city = ?, state = ?, postal_code = ?, country = ?, tags = ?,
			custom_fields = ?, updated_at = ? WHERE id = ?`,
		cur.Type, cur.FirstName, cur.LastName, cur.CompanyName, cur.Email, cur.Phone, cur.Website,
		cur.Address.Street, cur.Address.City, cur.Address.State, cur.Address.PostalCode, cur.Address.Country,
		jsonText(cur.Tags, "[]"), jsonText(cur.CustomFields, "{}"), now, id)
	if err != nil {
		return err
	}
	name := displayName(cur.FirstName, cur.LastName, cur.CompanyName)
	if err := logActivity(ctx, tx, actor, "contact_updated", "Contact updated: "+name,
		activityRefs{contactID: id}, map[string]any{"changedFields": nonNilStrings(changedContactFields(prev, cur))}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getContact(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *app) deleteContact(w http.ResponseWriter, r *http.Request) error {
	now := nowStamp()
	res, err := a.db.ExecContext(r.Context(),
		`UPDATE contacts SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now, now, r.PathValue("id"))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Contact")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *app) getContactDetail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	c, err := a.getContact(ctx, id)
	if err != nil {
		return err
	}
	deals, err := a.dealBriefs(ctx, "d.contact_id = ?", id)
	if err != nil {
		return err
	}
	tasks, err := a.taskBriefs(ctx, "contact_id", id)
	if err != nil {
		return err
	}
	notes, _, err := a.listNotes(ctx, " WHERE n.contact_id = ?", []any{id}, " ORDER BY n.created_at DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	acts, _, err := a.listActivities(ctx, " WHERE a.contact_id = ?", []any{id}, " ORDER BY a.created_at DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, contactDetail{contactOut: c, Deals: deals, Tasks: tasks, Notes: notes, Activities: acts})
	return nil
}

// nonNilStrings keeps an empty result encoding as [] rather than null.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
