package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

var (
	taskTypes      = []string{"call", "email", "meeting", "follow_up", "other"}
	taskStatuses   = []string{"open", "in_progress", "completed", "cancelled"}
	taskPriorities = []string{"low", "medium", "high"}
)

type reminderOut struct {
	Type          string `json:"type"`
	MinutesBefore int    `json:"minutesBefore"`
}

type taskRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type relatedTo struct {
	Contact *contactRef `json:"contact,omitempty"`
	Deal    *dealRef    `json:"deal,omitempty"`
	Task    *taskRef    `json:"task,omitempty"`
}

type taskOut struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	Priority     string         `json:"priority"`
	AssignedTo   userRef        `json:"assignedTo"`
	RelatedTo    relatedTo      `json:"relatedTo"`
	DueDate      *string        `json:"dueDate"`
	CompletedAt  *string        `json:"completedAt"`
	Reminder     *reminderOut   `json:"reminder"`
	CustomFields map[string]any `json:"customFields"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`

	// Raw link ids are kept so an update does not drop a link whose
	// contact or deal was soft-deleted (the joined ref would be nil).
	contactID string
	dealID    string
}

type taskBrief struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Status  string  `json:"status"`
	DueDate *string `json:"dueDate"`
}

type relatedInput struct {
	ContactID *string `json:"contactId"`
	DealID    *string `json:"dealId"`
}

type taskInput struct {
	Title        *string         `json:"title"`
	Description  *string         `json:"description"`
	Type         *string         `json:"type"`
	Status       *string         `json:"status"`
	Priority     *string         `json:"priority"`
	AssignedToID *string         `json:"assignedToId"`
	RelatedTo    *relatedInput   `json:"relatedTo"`
	DueDate      *string         `json:"dueDate"`
	CompletedAt  *string         `json:"completedAt"`
	Reminder     *reminderOut    `json:"reminder"`
	CustomFields *map[string]any `json:"customFields"`
}

const (
	taskCols = `SELECT t.id, t.title, t.description, t.type, t.status, t.priority, t.due_date, t.completed_at,
		t.reminder_type, t.reminder_minutes, t.custom_fields, t.created_at, t.updated_at,
		t.contact_id, t.deal_id, u.id, u.first_name, u.last_name,
		c.id, c.first_name, c.last_name, d.id, d.title`
	taskFrom = `FROM tasks t
		JOIN users u ON u.id = t.assigned_to_id
		LEFT JOIN contacts c ON c.id = t.contact_id AND c.deleted_at IS NULL
		LEFT JOIN deals d ON d.id = t.deal_id AND d.deleted_at IS NULL`
)

func scanTask(s rowScanner) (taskOut, error) {
	var t taskOut
	var due, completed, rtype sql.NullString
	var rmin sql.NullInt64
	var custom string
	var tcid, tdid sql.NullString
	var ccid, cfirst, clast, did, dtitle sql.NullString
	err := s.Scan(&t.ID, &t.Title, &t.Description, &t.Type, &t.Status, &t.Priority, &due, &completed,
		&rtype, &rmin, &custom, &t.CreatedAt, &t.UpdatedAt,
		&tcid, &tdid, &t.AssignedTo.ID, &t.AssignedTo.FirstName, &t.AssignedTo.LastName,
		&ccid, &cfirst, &clast, &did, &dtitle)
	if err != nil {
		return t, err
	}
	t.DueDate = strPtr(due)
	t.CompletedAt = strPtr(completed)
	if rtype.Valid {
		t.Reminder = &reminderOut{Type: rtype.String, MinutesBefore: int(rmin.Int64)}
	}
	t.CustomFields = map[string]any{}
	_ = json.Unmarshal([]byte(custom), &t.CustomFields)
	t.contactID = tcid.String
	t.dealID = tdid.String
	if ccid.Valid {
		t.RelatedTo.Contact = &contactRef{ID: ccid.String, FirstName: cfirst.String, LastName: clast.String}
	}
	if did.Valid {
		t.RelatedTo.Deal = &dealRef{ID: did.String, Title: dtitle.String}
	}
	return t, nil
}

func (a *app) getTask(ctx context.Context, id string) (taskOut, error) {
	vis, va := andVisible(ctx, "t.assigned_to_id")
	row := a.db.QueryRowContext(ctx, taskCols+" "+taskFrom+" WHERE t.id = ?"+vis, append([]any{id}, va...)...)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound("Task")
	}
	return t, err
}

// taskBriefs lists a contact's or deal's tasks; column is a fixed internal name.
func (a *app) taskBriefs(ctx context.Context, column, id string) ([]taskBrief, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT id, title, status, due_date FROM tasks WHERE `+column+` = ?`+
			scopeSQL(ctx, "assigned_to_id")+` ORDER BY due_date LIMIT 50`, append([]any{id}, scopeArgs(ctx)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []taskBrief{}
	for rows.Next() {
		var t taskBrief
		var due sql.NullString
		if err := rows.Scan(&t.ID, &t.Title, &t.Status, &due); err != nil {
			return nil, err
		}
		t.DueDate = strPtr(due)
		out = append(out, t)
	}
	return out, rows.Err()
}

func applyTask(t *taskOut, in taskInput) {
	if in.Title != nil {
		t.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		t.Description = *in.Description
	}
	if in.Type != nil {
		t.Type = *in.Type
	}
	if in.Status != nil {
		t.Status = *in.Status
	}
	if in.Priority != nil {
		t.Priority = *in.Priority
	}
	if in.DueDate != nil {
		t.DueDate = strOrNilPtr(*in.DueDate)
	}
	if in.CompletedAt != nil {
		t.CompletedAt = strOrNilPtr(*in.CompletedAt)
	}
	if in.Reminder != nil {
		rem := *in.Reminder
		t.Reminder = &rem
	}
	if in.CustomFields != nil {
		t.CustomFields = *in.CustomFields
	}
}

func strOrNilPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func validateTask(t taskOut) error {
	if t.Title == "" {
		return errValidation("title", "Title is required")
	}
	if !contains(taskTypes, t.Type) {
		return errValidation("type", "Unknown task type")
	}
	if !contains(taskStatuses, t.Status) {
		return errValidation("status", "Unknown task status")
	}
	if !contains(taskPriorities, t.Priority) {
		return errValidation("priority", "Priority must be low, medium or high")
	}
	if t.Reminder != nil {
		if t.Reminder.Type != "email" && t.Reminder.Type != "notification" {
			return errValidation("reminder.type", "Reminder type must be email or notification")
		}
		if t.Reminder.MinutesBefore < 0 {
			return errValidation("reminder.minutesBefore", "Reminder minutes cannot be negative")
		}
	}
	return nil
}

func (a *app) checkLinks(ctx context.Context, contactID, dealID string) error {
	if contactID != "" {
		ok, err := a.contactExists(ctx, contactID)
		if err != nil {
			return err
		}
		if !ok {
			return errValidation("relatedTo.contactId", "Contact not found")
		}
	}
	if dealID != "" {
		vis, va := andVisible(ctx, "owner_id")
		var n int
		if err := a.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM deals WHERE id = ? AND deleted_at IS NULL`+vis, append([]any{dealID}, va...)...).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return errValidation("relatedTo.dealId", "Deal not found")
		}
	}
	return nil
}

var taskSortCols = map[string]string{
	"dueDate":   "t.due_date",
	"priority":  "CASE t.priority WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END",
	"createdAt": "t.created_at",
}

func (a *app) listTasks(w http.ResponseWriter, r *http.Request) error {
	p, err := parsePaging(r, 20, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	where := []string{"1=1"}
	args := []any{}

	if s := strings.TrimSpace(q.Get("search")); s != "" {
		where = append(where, "t.title LIKE ?")
		args = append(args, "%"+s+"%")
	}
	if st := csvParam(r, "status"); len(st) > 0 {
		if err := validateEnum(st, taskStatuses, "status"); err != nil {
			return err
		}
		where = append(where, "t.status IN ("+placeholders(len(st))+")")
		args = append(args, toAny(st)...)
	}
	if pr := csvParam(r, "priority"); len(pr) > 0 {
		if err := validateEnum(pr, taskPriorities, "priority"); err != nil {
			return err
		}
		where = append(where, "t.priority IN ("+placeholders(len(pr))+")")
		args = append(args, toAny(pr)...)
	}
	if ty := csvParam(r, "type"); len(ty) > 0 {
		if err := validateEnum(ty, taskTypes, "type"); err != nil {
			return err
		}
		where = append(where, "t.type IN ("+placeholders(len(ty))+")")
		args = append(args, toAny(ty)...)
	}
	if assignee := q.Get("assignedTo"); assignee != "" {
		where = append(where, "t.assigned_to_id = ?")
		args = append(args, assignee)
	}
	where, args = addVisible(r.Context(), where, args, "t.assigned_to_id")
	if from := q.Get("dueDateFrom"); from != "" {
		if from == "today" {
			from = todayStamp()
		}
		where = append(where, "date(t.due_date) >= date(?)")
		args = append(args, from)
	}
	if to := q.Get("dueDateTo"); to != "" {
		where = append(where, "date(t.due_date) <= date(?)")
		args = append(args, to)
	}

	order, err := orderClause(r, taskSortCols, "dueDate", "asc", "t.id")
	if err != nil {
		return err
	}
	items, total, err := queryPage(r.Context(), a.db, taskCols, taskFrom,
		" WHERE "+strings.Join(where, " AND "), order, args, p, scanTask)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "pagination": newPagination(p, total)})
	return nil
}

func (a *app) getTaskHandler(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	t, err := a.getTask(ctx, id)
	if err != nil {
		return err
	}
	acts, _, err := a.listActivities(ctx, " WHERE a.task_id = ?", []any{id}, " ORDER BY a.created_at DESC, a.rowid DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	notes, _, err := a.listNotes(ctx, " WHERE n.task_id = ?"+scopeSQL(ctx, "n.author_id"), append([]any{id}, scopeArgs(ctx)...), " ORDER BY n.created_at DESC, n.rowid DESC", pageParams{1, 50})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, struct {
		taskOut
		Activities []activityOut `json:"activities"`
		Notes      []noteOut     `json:"notes"`
	}{t, acts, notes})
	return nil
}

func (a *app) createTask(w http.ResponseWriter, r *http.Request) error {
	var in taskInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	actor := currentUserID(r)

	t := taskOut{Type: "other", Status: "open", Priority: "medium", CustomFields: map[string]any{}}
	applyTask(&t, in)

	assignee := actor
	if in.AssignedToID != nil && *in.AssignedToID != "" {
		assignee = *in.AssignedToID
	}
	ref, err := a.userRefFor(ctx, assignee, "assignedToId")
	if err != nil {
		return err
	}
	t.AssignedTo = ref

	if in.RelatedTo != nil {
		if in.RelatedTo.ContactID != nil {
			t.contactID = *in.RelatedTo.ContactID
		}
		if in.RelatedTo.DealID != nil {
			t.dealID = *in.RelatedTo.DealID
		}
	}
	if err := validateTask(t); err != nil {
		return err
	}
	if err := a.checkLinks(ctx, t.contactID, t.dealID); err != nil {
		return err
	}
	if !inScope(ctx, assignee) {
		return errForbidden("You can only assign tasks to people in your team")
	}

	id := newID()
	now := nowStamp()
	if t.Status == "completed" && t.CompletedAt == nil {
		s := now
		t.CompletedAt = &s
	}
	if t.Status != "completed" {
		t.CompletedAt = nil
	}
	var rtype, rmin any
	if t.Reminder != nil {
		rtype = t.Reminder.Type
		rmin = t.Reminder.MinutesBefore
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tasks (id, title, description, type, status, priority, assigned_to_id, contact_id, deal_id,
			due_date, completed_at, reminder_type, reminder_minutes, custom_fields, created_at, updated_at)
		 VALUES (`+placeholders(16)+`)`,
		id, t.Title, t.Description, t.Type, t.Status, t.Priority, assignee,
		nilIfEmpty(t.contactID), nilIfEmpty(t.dealID), strOrNil(ptrOrNil(t.DueDate)), strOrNil(ptrOrNil(t.CompletedAt)),
		rtype, rmin, jsonText(t.CustomFields, "{}"), now, now)
	if err != nil {
		return err
	}
	if assignee != actor {
		if err := logActivity(ctx, tx, actor, "task_assigned", "Task assigned: "+t.Title,
			activityRefs{taskID: id, contactID: t.contactID, dealID: t.dealID},
			map[string]any{"assignedTo": assignee}); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getTask(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, out)
	return nil
}

func ptrOrNil(p *string) *string { return p }

func (a *app) updateTask(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	cur, err := a.getTask(ctx, id)
	if err != nil {
		return err
	}
	var in taskInput
	if err := decodeBody(w, r, &in); err != nil {
		return err
	}
	prevStatus := cur.Status
	applyTask(&cur, in)

	if in.AssignedToID != nil && *in.AssignedToID != "" {
		ref, err := a.userRefFor(ctx, *in.AssignedToID, "assignedToId")
		if err != nil {
			return err
		}
		cur.AssignedTo = ref
	}
	if in.RelatedTo != nil {
		if in.RelatedTo.ContactID != nil {
			cur.contactID = *in.RelatedTo.ContactID
		}
		if in.RelatedTo.DealID != nil {
			cur.dealID = *in.RelatedTo.DealID
		}
	}
	if err := validateTask(cur); err != nil {
		return err
	}
	if err := a.checkLinks(ctx, cur.contactID, cur.dealID); err != nil {
		return err
	}
	if !inScope(ctx, cur.AssignedTo.ID) {
		return errForbidden("You can only assign tasks to people in your team")
	}

	actor := currentUserID(r)
	now := nowStamp()
	if cur.Status == "completed" && cur.CompletedAt == nil {
		s := now
		cur.CompletedAt = &s
	}
	if cur.Status != "completed" {
		cur.CompletedAt = nil
	}
	justCompleted := cur.Status == "completed" && prevStatus != "completed"

	var rtype, rmin any
	if cur.Reminder != nil {
		rtype = cur.Reminder.Type
		rmin = cur.Reminder.MinutesBefore
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx,
		`UPDATE tasks SET title = ?, description = ?, type = ?, status = ?, priority = ?, assigned_to_id = ?,
			contact_id = ?, deal_id = ?, due_date = ?, completed_at = ?, reminder_type = ?, reminder_minutes = ?,
			custom_fields = ?, updated_at = ? WHERE id = ?`,
		cur.Title, cur.Description, cur.Type, cur.Status, cur.Priority, cur.AssignedTo.ID,
		nilIfEmpty(cur.contactID), nilIfEmpty(cur.dealID), strOrNil(ptrOrNil(cur.DueDate)),
		strOrNil(ptrOrNil(cur.CompletedAt)), rtype, rmin, jsonText(cur.CustomFields, "{}"), now, id)
	if err != nil {
		return err
	}
	if justCompleted {
		if err := logActivity(ctx, tx, actor, "task_completed", "Task completed: "+cur.Title,
			activityRefs{taskID: id, contactID: cur.contactID, dealID: cur.dealID}, nil); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	out, err := a.getTask(ctx, id)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func (a *app) deleteTask(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	if _, err := a.getTask(ctx, id); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Tasks are hard-deleted; detach activity and note history first so
	// foreign keys do not block the delete and the audit trail survives.
	if _, err := tx.ExecContext(ctx, `UPDATE activities SET task_id = NULL WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notes SET task_id = NULL WHERE task_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("Task")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (a *app) myTasks(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	uid := currentUserID(r)
	statuses := csvParam(r, "status")
	if len(statuses) == 0 {
		statuses = []string{"open", "in_progress"}
	}
	if err := validateEnum(statuses, taskStatuses, "status"); err != nil {
		return err
	}
	where := []string{"t.assigned_to_id = ?", "t.status IN (" + placeholders(len(statuses)) + ")"}
	args := append([]any{uid}, toAny(statuses)...)
	if from := r.URL.Query().Get("dueDateFrom"); from != "" {
		if from == "today" {
			from = todayStamp()
		}
		where = append(where, "date(t.due_date) >= date(?)")
		args = append(args, from)
	}
	order, err := orderClause(r, taskSortCols, "dueDate", "asc", "t.id")
	if err != nil {
		return err
	}
	items, _, err := queryPage(ctx, a.db, taskCols, taskFrom,
		" WHERE "+strings.Join(where, " AND "), order, args, pageParams{1, 100}, scanTask)
	if err != nil {
		return err
	}

	// Due-date buckets are counted over the user's open work, regardless of the status filter.
	today := todayStamp()
	tomorrow := nextDay(today)
	weekOut := daysAfter(today, 7)
	var overdue, dueToday, dueSoon int
	err = a.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(CASE WHEN date(t.due_date) < ? THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN date(t.due_date) = ? THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN date(t.due_date) >= ? AND date(t.due_date) <= ? THEN 1 ELSE 0 END), 0)
		FROM tasks t
		WHERE t.assigned_to_id = ? AND t.status IN ('open', 'in_progress') AND t.due_date IS NOT NULL`,
		today, today, tomorrow, weekOut, uid).Scan(&overdue, &dueToday, &dueSoon)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":     items,
		"overdue":  overdue,
		"dueToday": dueToday,
		"dueSoon":  dueSoon,
	})
	return nil
}
