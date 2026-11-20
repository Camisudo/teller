# Teller Deployment Guide

This guide covers how to deploy Teller as a production service on Linux, macOS, and Windows.

## Overview

Teller is a single, self-contained executable that bundles the entire CRM application:
- React web UI (compiled)
- Go backend API
- SQLite database

No external dependencies are required. It is suitable for on-premises deployment or as a downloadable application.

## System Requirements

### Minimum
- **CPU**: 1 core
- **RAM**: 256 MB
- **Disk**: 100 MB for binary + variable space for data (database, logs)
- **Network**: None required (runs entirely locally unless exposed)

### Recommended
- **CPU**: 2+ cores
- **RAM**: 1 GB
- **Disk**: 1 GB or more (depending on data volume)

### Supported Operating Systems
- Linux (glibc and musl, x86_64)
- macOS (Intel and Apple Silicon, via Rosetta)
- Windows 10/11 (x86_64)

## Installation

### Linux

1. **Download** the binary:
   ```bash
   curl -O https://releases.example.com/teller-linux-amd64
   chmod +x teller-linux-amd64
   ```

2. **Place it** in a system directory:
   ```bash
   sudo mv teller-linux-amd64 /opt/teller/bin/teller
   ```

3. **Create a data directory** (optional; created on first run):
   ```bash
   sudo mkdir -p /var/lib/teller
   sudo chown teller:teller /var/lib/teller
   ```

### macOS

1. **Download** and move to Applications:
   ```bash
   curl -O https://releases.example.com/teller-macos-amd64
   chmod +x teller-macos-amd64
   sudo mv teller-macos-amd64 /Applications/Teller/
   ```

2. **Grant execute permission** (if macOS complains about untrusted developer):
   ```bash
   xattr -d com.apple.quarantine /Applications/Teller/teller
   ```

### Windows

1. **Download** `teller.exe` from the releases page.
2. **Place it** in a folder, e.g. `C:\Program Files\Teller\`.
3. The data directory will be created as `data/` next to the executable on first run.

## Running Teller

### Standalone (Development / Testing)

```bash
./teller
```

The binary will:
1. Create a `data/` directory next to itself if it doesn't exist.
2. Initialize the SQLite database and JWT signing key.
3. Create an admin account and write the generated password to `teller-admin-password.txt`.
4. Open a browser window to `http://localhost:3000`.
5. Listen on `http://localhost:3000`.

**Important**: After signing in, delete `teller-admin-password.txt` to prevent the password from being exposed.

### With Custom Data Directory

```bash
./teller -data /path/to/data
```

Useful for running multiple instances or storing data on a network drive.

### Without Auto-opening Browser

```bash
./teller -no-browser
```

Useful when running on a server without a display.

### On a Custom Port

```bash
./teller -addr :8080
```

Listens on port 8080 instead of the default 3000.

### Reset Admin Password

```bash
./teller -reset-admin-password
```

Or provide the password via environment variable:
```bash
TELLER_ADMIN_PASSWORD="newpassword" ./teller -reset-admin-password
```

The password is printed to `teller-admin-password.txt` or logged to stdout (if the env var is used).

## Production Setup

### Linux with Systemd

Create a service file at `/etc/systemd/system/teller.service`:

```ini
[Unit]
Description=Teller CRM
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=teller
Group=teller
WorkingDirectory=/opt/teller
ExecStart=/opt/teller/bin/teller -no-browser -addr :3000 -data /var/lib/teller
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

# Security
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/teller

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable teller
sudo systemctl start teller
```

Check status:
```bash
sudo systemctl status teller
journalctl -u teller -f  # follow logs
```

### Windows Service (via NSSM)

Install [NSSM](https://nssm.cc/) then register Teller as a service:

```cmd
nssm install Teller "C:\Program Files\Teller\teller.exe" "-no-browser -addr :3000 -data C:\ProgramData\Teller"
nssm start Teller
```

Verify:
```cmd
nssm status Teller
```

### macOS with Launchd

Create `/Library/LaunchDaemons/com.example.teller.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.example.teller</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Applications/Teller/teller</string>
		<string>-no-browser</string>
		<string>-addr</string>
		<string>:3000</string>
		<string>-data</string>
		<string>/Library/Application Support/Teller</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>/var/log/teller.log</string>
	<key>StandardErrorPath</key>
	<string>/var/log/teller.log</string>
</dict>
</plist>
```

Load and start:
```bash
sudo launchctl load /Library/LaunchDaemons/com.example.teller.plist
sudo launchctl start com.example.teller
```

## Network Access

### Local Only (Default)

Teller listens on `127.0.0.1:3000` by default. Only local connections are allowed.

To allow access from other machines:

```bash
./teller -addr 0.0.0.0:3000
```

**Warning**: This exposes Teller to the network without encryption. Only do this on a private network or behind a reverse proxy with TLS.

### Behind a Reverse Proxy (Nginx Example)

```nginx
upstream teller {
    server 127.0.0.1:3000;
}

server {
    listen 443 ssl http2;
    server_name crm.example.com;

    ssl_certificate /etc/ssl/certs/crm.example.com.crt;
    ssl_certificate_key /etc/ssl/private/crm.example.com.key;

    location / {
        proxy_pass http://teller;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

This allows Teller to serve HTTPS at `https://crm.example.com` while running locally on port 3000.

## Backup and Recovery

### Backing Up Data

The data directory contains all user data. Back it up regularly:

```bash
# Backup to a file
tar -czf teller-backup-$(date +%Y%m%d-%H%M%S).tar.gz /var/lib/teller

# Or copy to another location
rsync -av /var/lib/teller /mnt/backup/
```

### Restoring from Backup

1. Stop Teller:
   ```bash
   sudo systemctl stop teller
   ```

2. Restore the data directory:
   ```bash
   tar -xzf teller-backup-20240101-120000.tar.gz -C /
   ```

3. Start Teller:
   ```bash
   sudo systemctl start teller
   ```

## Monitoring and Logging

### Systemd Journal

View recent logs:
```bash
journalctl -u teller -n 50
```

Follow logs in real time:
```bash
journalctl -u teller -f
```

### Manual Logging

If running Teller manually, redirect stdout to a log file:
```bash
./teller > /var/log/teller.log 2>&1 &
```

### Key Log Messages

- `Teller running at http://...` — Server started successfully.
- `First run: admin account created.` — Initial setup completed.
- `Admin password reset.` — Admin password was reset.
- `shutting down` — Server shutting down gracefully.

## Troubleshooting

### "Port already in use"

Another process is listening on port 3000. Either:
- Kill the other process
- Use `-addr :8080` to listen on a different port
- Identify what's using the port: `lsof -i :3000` (Linux/macOS)

### "Permission denied" (Linux)

Teller needs read/write access to the data directory:
```bash
sudo chown -R teller:teller /var/lib/teller
sudo chmod 700 /var/lib/teller
```

### "Database locked"

Teller is running multiple instances on the same data directory. Only one instance can use a data directory at a time. Use `-data` to point each instance to a different directory.

### Admin password lost

Reset it:
```bash
./teller -reset-admin-password
```

## Security Best Practices

1. **Keep the data directory private**: It contains the JWT signing key. Only the Teller process should access it.
   ```bash
   chmod 700 /var/lib/teller
   ```

2. **Use HTTPS**: Always run behind a reverse proxy with TLS/SSL on a production network.

3. **Change the admin password**: The initial password is randomly generated. After first login, change it via the UI.

4. **Regular backups**: Store encrypted backups of the data directory offline.

5. **Firewall**: Restrict network access to Teller to trusted machines.
   ```bash
   sudo ufw allow from 192.168.1.0/24 to any port 3000  # Allow from internal subnet only
   ```

6. **Monitor logs**: Watch for failed login attempts or errors.

## Updating Teller

1. Download the new binary.
2. Stop the running instance: `sudo systemctl stop teller` (or kill the process).
3. Replace the old binary with the new one.
4. Start Teller: `sudo systemctl start teller`.

The data directory is automatically migrated if the schema changes.

## Support

For issues, consult the README and logs. Contact support with the output of:
```bash
./teller -help
journalctl -u teller -n 100
```
