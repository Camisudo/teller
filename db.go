package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver: no cgo, so Windows builds cross-compile cleanly
)

func openDB(path string) (*sql.DB, error) {
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT NOT NULL,
	first_name TEXT NOT NULL,
	last_name TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT 'sales_rep',
	avatar TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS contacts (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL DEFAULT 'individual',
	first_name TEXT NOT NULL,
	last_name TEXT NOT NULL DEFAULT '',
	company_name TEXT NOT NULL DEFAULT '',
	email TEXT NOT NULL,
	phone TEXT NOT NULL DEFAULT '',
	website TEXT NOT NULL DEFAULT '',
	street TEXT NOT NULL DEFAULT '',
	city TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT '',
	postal_code TEXT NOT NULL DEFAULT '',
	country TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	custom_fields TEXT NOT NULL DEFAULT '{}',
	owner_id TEXT NOT NULL REFERENCES users(id),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_contacts_email ON contacts(lower(email)) WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS deals (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	value REAL NOT NULL DEFAULT 0,
	currency TEXT NOT NULL DEFAULT 'USD',
	stage TEXT NOT NULL,
	probability INTEGER NOT NULL DEFAULT 50,
	contact_id TEXT REFERENCES contacts(id),
	owner_id TEXT NOT NULL REFERENCES users(id),
	expected_close_date TEXT,
	closed_date TEXT,
	closed_reason TEXT,
	tags TEXT NOT NULL DEFAULT '[]',
	custom_fields TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	deleted_at TEXT
);
CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	type TEXT NOT NULL DEFAULT 'other',
	status TEXT NOT NULL DEFAULT 'open',
	priority TEXT NOT NULL DEFAULT 'medium',
	assigned_to_id TEXT NOT NULL REFERENCES users(id),
	contact_id TEXT REFERENCES contacts(id),
	deal_id TEXT REFERENCES deals(id),
	due_date TEXT,
	completed_at TEXT,
	reminder_type TEXT,
	reminder_minutes INTEGER,
	custom_fields TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS activities (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	description TEXT NOT NULL,
	actor_id TEXT NOT NULL REFERENCES users(id),
	contact_id TEXT REFERENCES contacts(id),
	deal_id TEXT REFERENCES deals(id),
	task_id TEXT REFERENCES tasks(id),
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notes (
	id TEXT PRIMARY KEY,
	content TEXT NOT NULL,
	author_id TEXT NOT NULL REFERENCES users(id),
	contact_id TEXT REFERENCES contacts(id),
	deal_id TEXT REFERENCES deals(id),
	task_id TEXT REFERENCES tasks(id),
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_activities_created ON activities(created_at);
CREATE INDEX IF NOT EXISTS ix_tasks_assignee ON tasks(assigned_to_id, status);
`

func migrate(db *sql.DB) error {
	for _, stmt := range strings.Split(schema, ";") {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("%w (statement: %.60s)", err, strings.TrimSpace(stmt))
		}
	}
	// Columns added after the first release. CREATE TABLE IF NOT EXISTS does not
	// alter existing tables, so add them here when missing.
	if err := ensureColumn(db, "users", "active", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	// manager_id links a rep to their manager. NULL means no manager.
	if err := ensureColumn(db, "users", "manager_id", "TEXT"); err != nil {
		return err
	}
	// Set by an admin password reset; cleared when the user changes their password.
	if err := ensureColumn(db, "users", "must_change_password", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
}
