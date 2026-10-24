package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const credentialFileName = "teller-admin-password.txt"

// credentialFilePath is next to the executable, so a Windows user who
// double-clicks teller.exe (no console window) can still find the password.
// Falls back to the data directory if the executable's folder can't be found.
func credentialFilePath(dataDir string) string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), credentialFileName)
	}
	return filepath.Join(dataDir, credentialFileName)
}

// writeCredentialFile records a generated admin password. CRLF line endings
// keep the file readable in Notepad on Windows.
func writeCredentialFile(path, password string) error {
	content := fmt.Sprintf(
		"Teller admin account\r\n"+
			"Email:    %s\r\n"+
			"Password: %s\r\n\r\n"+
			"Sign in, then delete this file. Teller will not show this password again.\r\n",
		adminEmail, password)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
