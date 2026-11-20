# Teller Architecture

This document describes the design and components of Teller.

## Overview

Teller is a single-binary CRM application composed of:

1. **Frontend** (React + TypeScript): Web UI for contacts, deals, tasks, and reports.
2. **API** (Go): RESTful backend serving the UI and handling business logic.
3. **Database** (SQLite): Persistent storage for all application data.

The entire application is compiled into one executable that runs on any OS without external dependencies.

## System Architecture

```
┌─────────────────────────────────────┐
│         HTTP Client Browser         │
└──────────────────┬──────────────────┘
                   │ HTTP (port 3000)
                   ▼
┌─────────────────────────────────────┐
│   Single Binary Executable (teller)  │
├─────────────────────────────────────┤
│  Frontend Bundle (compiled React)    │  Serves index.html, JS, CSS
├─────────────────────────────────────┤
│  Go HTTP Server                     │
├─────────────────────────────────────┤
│  API Routes (/v1/...)               │  REST endpoints
├─────────────────────────────────────┤
│  SQLite Database (/var/lib/teller)  │  User data, auth, CRM entities
└─────────────────────────────────────┘
```

## Frontend

Located in `/home/guiness/crm-frontend`.

- **Framework**: React 19 with TypeScript
- **Build tool**: Vite (compiles to dist/, then embedded in Go binary)
- **State management**: 
  - Zustand for auth state (token, user, UI modals, notifications)
  - TanStack Query for server state (contacts, deals, tasks, activities)
- **Styling**: Tailwind CSS v4 with design tokens

**Key Components**:
- `LoginPage`: Email + password authentication
- `DashboardLayout`: Main app shell with sidebar, header, and routes
- `ContactsPage`, `DealsPage`, `TasksPage`, `ActivitiesPage`, `ReportsPage`: Feature pages
- `Modal`, `DeleteConfirmModal`: Reusable UI patterns
- `api.ts`: Axios HTTP client with request/response interceptors

**Build**: `npm run build` produces optimized bundles in `dist/`, then the Go build.sh copies them into `web/dist/` for embedding.

## Backend

Located in `/home/guiness/teller`.

- **Language**: Go
- **HTTP**: stdlib net/http with custom routing (Go 1.22+)
- **Database**: SQLite with sql.DB
- **Auth**: JWT tokens, Bearer scheme
- **Data models**: User, Contact, Deal, Task, Activity, Note

### Go Modules

**main.go**: Entry point, CLI flags, server lifecycle, signal handling.

**db.go**: Database initialization, schema, JWT key management.

**auth.go**: Login, token refresh, password reset, role-based authorization.

**contacts.go**: CRUD for contacts, notes attached to contacts.

**deals.go**: CRUD for deals, pipeline filtering by stage.

**tasks.go**: CRUD for tasks, status/priority filtering, completion tracking.

**activities.go**: Activity log (read-only from the UI; created by other endpoints).

**users.go**: User management (admin only), password reset.

**scope.go**: Query filtering to enforce team/owner scoping (each user sees only their data).

**common.go**: HTTP helpers, error responses, pagination.

**web.go**: SPA handler (serves index.html for all unknown routes).

### API Routes

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| POST | `/v1/auth/login` | None | Login with email/password |
| POST | `/v1/auth/logout` | Required | Logout (clears token) |
| POST | `/v1/auth/refresh` | Required | Refresh JWT token |
| POST | `/v1/auth/password-reset` | None | Reset password (email link) |
| POST | `/v1/auth/change-password` | Required | Change own password |
| GET | `/v1/users` | Admin | List users |
| POST | `/v1/users` | Admin | Create user |
| PATCH | `/v1/users/{id}` | Admin | Update user |
| POST | `/v1/users/{id}/reset-password` | Admin | Reset user password |
| GET | `/v1/contacts` | Required | List contacts (scoped) |
| POST | `/v1/contacts` | Required | Create contact |
| GET | `/v1/contacts/{id}` | Required | Get contact detail |
| PATCH | `/v1/contacts/{id}` | Required | Update contact |
| DELETE | `/v1/contacts/{id}` | Required | Delete contact |
| GET | `/v1/contacts/{id}/notes` | Required | List notes for contact |
| POST | `/v1/contacts/{id}/notes` | Required | Add note to contact |
| GET | `/v1/deals` | Required | List deals (scoped, filterable) |
| POST | `/v1/deals` | Required | Create deal |
| GET | `/v1/deals/{id}` | Required | Get deal detail |
| PATCH | `/v1/deals/{id}` | Required | Update deal |
| DELETE | `/v1/deals/{id}` | Required | Delete deal |
| GET | `/v1/tasks` | Required | List tasks (scoped, filterable) |
| POST | `/v1/tasks` | Required | Create task |
| GET | `/v1/tasks/{id}` | Required | Get task detail |
| PATCH | `/v1/tasks/{id}` | Required | Update task |
| DELETE | `/v1/tasks/{id}` | Required | Delete task |
| GET | `/v1/activities` | Required | List activities |
| GET | `/v1/activities/{id}` | Required | Get activity detail |
| PATCH | `/v1/notes/{id}` | Required | Update note |
| DELETE | `/v1/notes/{id}` | Required | Delete note |
| GET | `/v1/dashboard/metrics` | Required | Dashboard summary stats |
| GET | `/v1/dashboard/my-tasks` | Required | Current user's tasks |
| GET | `/` | None | Serve UI (SPA) |

### Request/Response Format

**Success Response** (200, 201):
```json
{
  "data": { /* resource or array */ },
  "pagination": { /* if list */ }
}
```

**Error Response** (4xx, 5xx):
```json
{
  "error": {
    "code": "INVALID_INPUT",
    "message": "Email is required",
    "details": { /* optional */ }
  }
}
```

### Authentication

1. User logs in with email + password → `POST /v1/auth/login` → returns `{ user, token, expiresIn }`.
2. Frontend stores token in localStorage.
3. Frontend sends `Authorization: Bearer <token>` with every request.
4. Backend validates token signature (using JWT key from data dir).
5. 401 response → frontend clears token and redirects to `/login`.

Tokens are signed with HMAC-SHA256 using a 256-bit key stored in `data/jwt.key` (created on first run).

### Authorization

**Roles**: Admin, SalesManager, SalesRep, Customer.

**Admin-only endpoints**: `/v1/users/*`, password reset.

**Protected endpoints**: Most CRUD endpoints require a valid token. The user is extracted from the token and enforced via row-level filtering (see scope.go).

### Data Scoping

Each user sees only:
- **Contacts**: Their own + team members' contacts
- **Deals**: Their own + team members' deals
- **Tasks**: Their own + team members' tasks
- **Activities**: Their own + team members' activities

This is enforced at the query level in `scope.go`.

## Database

SQLite file: `data/teller.db`

**Schema**:
- `users`: Email, hashed password, role, timestamps.
- `contacts`: Name, email, phone, company, type (individual/company), owner, timestamps.
- `deals`: Title, value, stage (prospect/qualified/proposal/negotiation/won/lost), contact, owner, timestamps.
- `tasks`: Title, type (call/email/meeting/follow_up/other), status (open/in_progress/completed/cancelled), priority (low/medium/high), assigned_to, timestamps.
- `activities`: Type (contact_created, deal_stage_changed, etc.), description, actor, timestamps.
- `notes`: Content, author, related_to (contact/deal/task), timestamps.

Migrations are applied on startup if the schema version is older than the binary's version.

## Build & Deployment

**Build Steps** (see `build.sh`):

1. Run `npm run build` in the React frontend → `crm-frontend/dist/`
2. Copy frontend files to `teller/web/dist/`
3. Run `go build` with `-ldflags="-s -w"` (strip symbols) → single binary
4. Compile for Linux, Windows (via CGO_ENABLED=0 for portability)

**Output**: `dist/teller` (Linux) and `dist/teller.exe` (Windows), each ~12 MB.

**Embedding**: React bundle is embedded into the Go binary at build time using `//go:embed` directives.

## Security Considerations

1. **JWT Key**: Stored unencrypted in `data/jwt.key`. Treat as a secret. Backup offline.
2. **Passwords**: Hashed using bcrypt (cost factor 12) in the database.
3. **SQL Injection**: All queries use parameterized statements.
4. **CORS**: Not explicitly set; frontend and API share the same origin.
5. **TLS**: Not enforced by default. Run behind a reverse proxy for HTTPS.
6. **Admin Password**: Generated on first run, written to file or environment variable.

## Performance

- **Concurrency**: Go's goroutines handle multiple requests.
- **Caching**: Frontend caches data via TanStack Query (5-min stale time).
- **Database**: SQLite is single-writer, but sufficient for typical CRM workloads (<1000 concurrent users).
- **Bundle Size**: ~400 KB gzipped (React + app code), ~12 MB binary.

## Known Limitations

1. **Scalability**: SQLite can handle typical small-to-medium deployments. For >100 concurrent users, consider PostgreSQL.
2. **Clustering**: No built-in clustering or replication. Only one instance can use a data directory.
3. **Real-time**: No WebSocket support. UI updates via polling and mutations.
4. **Testing**: Frontend has unit tests; backend lacks automated tests.
5. **Admin UI**: No admin panel for user management; only CLI and API.

## Future Enhancements

- PostgreSQL support
- Audit log
- Multi-tenancy
- Real-time updates
- Mobile app
- API documentation (OpenAPI/Swagger)
