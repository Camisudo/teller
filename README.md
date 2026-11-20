# Teller

A self-contained, single-binary CRM application for contact, deal, task, and pipeline management. No dependencies, no servers—just download and run.

## Quick Start

```bash
./teller
```

The app opens at `http://localhost:3000`. On first run, an admin account is created with a generated password printed to the terminal (or saved to `teller-admin-password.txt`). After signing in, delete that file and change your password.

**Supported**: Linux, macOS, Windows. Runs on any machine with no installation or external dependencies.

## Features

- **Contacts**: Manage individuals and companies with email, phone, and notes.
- **Deals**: Track sales opportunities through a visual pipeline (Kanban).
- **Tasks**: Create tasks (calls, emails, meetings, follow-ups) and assign to team members.
- **Activity Log**: See all changes in the system in one place.
- **Reports**: Dashboard metrics and pipeline analysis.
- **User Management**: Admin tools to create users and reset passwords.
- **Role-Based Access**: Admin, Sales Manager, Sales Rep, and Customer roles.
- **Team Scoping**: Users see only their own and their team's data.

## Documentation

- **[USER_GUIDE.md](USER_GUIDE.md)** — How to use Teller as an end user (contacts, deals, tasks, reports).
- **[DEPLOYMENT.md](DEPLOYMENT.md)** — How to deploy Teller in production (Linux, macOS, Windows; systemd; Nginx reverse proxy).
- **[ARCHITECTURE.md](ARCHITECTURE.md)** — Technical design (frontend, API, database, build).

## System Requirements

- **CPU**: 1 core minimum; 2+ recommended
- **RAM**: 256 MB minimum; 1 GB recommended
- **Disk**: 100 MB for binary; variable for data
- **Network**: None required (runs locally; expose via reverse proxy for remote access)

## Installation & Usage

### Standalone

```bash
./teller                        # Start on http://localhost:3000
./teller -addr :8080            # Custom port
./teller -data /path/to/data    # Custom data directory
./teller -no-browser            # Don't auto-open browser (for servers)
./teller -reset-admin-password  # Reset or set admin password
```

### Production (Linux with Systemd)

```bash
sudo mkdir -p /opt/teller/bin /var/lib/teller
sudo mv teller /opt/teller/bin/

# Create /etc/systemd/system/teller.service (see DEPLOYMENT.md)
sudo systemctl enable teller
sudo systemctl start teller
journalctl -u teller -f  # View logs
```

## Building from Source

Requires Go 1.22+ and Node.js 20+.

```bash
cd /path/to/teller
./build.sh
# Output: dist/teller (Linux), dist/teller.exe (Windows)
```

The build script compiles the React frontend, embeds it in the Go binary, and produces self-contained executables.

## Security

- Passwords are hashed with bcrypt (cost factor 12).
- JWT tokens signed with HMAC-SHA256.
- JWT signing key stored in `data/jwt.key` (must be backed up).
- SQL queries use parameterized statements (no injection risk).
- **Important**: Run behind a reverse proxy with TLS/SSL for production. By default, Teller serves HTTP only.

See [DEPLOYMENT.md](DEPLOYMENT.md) for securing Teller in production (HTTPS, firewall rules, backup).

## Architecture

Teller is a **single binary** that includes:

1. **Frontend**: React web app (compiled)
2. **Backend API**: Go HTTP server with business logic
3. **Database**: SQLite (included; no external DB needed)

All three components run in one process. No separate services, no configuration files—just the executable and a data directory.

For a detailed breakdown, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Deployment Options

- **Standalone**: Run on a laptop or PC as a desktop app.
- **Server**: Deploy on Linux with systemd and use as a service.
- **Containerized**: Build a Docker image (not provided, but straightforward).
- **Reverse Proxy**: Run behind Nginx/Apache for remote access with TLS.

See [DEPLOYMENT.md](DEPLOYMENT.md) for step-by-step instructions for each setup.

## Limitations

- **SQLite**: Suitable for small to medium deployments (<1000 concurrent users). For larger scale, switch to PostgreSQL.
- **Single Instance**: Only one Teller instance can use a data directory at a time.
- **No Clustering**: Built-in clustering and replication not supported.
- **Real-time Updates**: UI uses polling and mutations; no WebSocket support.

## License

TBD

## Support & Contributing

For bug reports, feature requests, or questions, contact support. When reporting issues, include:
- Teller version (`./teller -help` or check the binary's timestamp)
- Operating system and version
- Steps to reproduce
- Error messages or logs (`journalctl -u teller` for systemd)

## Development

The source code is organized as:

- `/crm-frontend` — React application (Vite, TanStack Query, Zustand)
- `/teller` — Go backend (stdlib HTTP, SQLite, JWT)

Both are built into a single binary by `build.sh`.

---

**Version 1.0** — Ready for production. See [DEPLOYMENT.md](DEPLOYMENT.md) to get started.
