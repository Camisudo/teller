package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

type app struct {
	db     *sql.DB
	jwtKey []byte
}

func main() {
	addr := flag.String("addr", ":3000", "listen address")
	dataFlag := flag.String("data", "", "data directory (default: data/ next to the executable)")
	noBrowser := flag.Bool("no-browser", false, "do not open a browser on start")
	resetAdmin := flag.Bool("reset-admin-password", false, "set a new admin password, then exit")
	flag.Parse()

	dir := resolveDataDir(*dataFlag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	db, err := openDB(filepath.Join(dir, "teller.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	key, err := loadOrCreateKey(filepath.Join(dir, "jwt.key"))
	if err != nil {
		log.Fatalf("jwt key: %v", err)
	}
	a := &app{db: db, jwtKey: key}

	if *resetAdmin {
		pw, err := chooseAdminPassword()
		if err != nil {
			log.Fatal(err)
		}
		if _, err := a.bootstrapAdmin(pw, true); err != nil {
			log.Fatalf("reset admin: %v", err)
		}
		if os.Getenv("TELLER_ADMIN_PASSWORD") == "" {
			if err := writeCredentialFile(credentialFilePath(dir), pw); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("Admin password reset. New password written to %s\n", credentialFilePath(dir))
		} else {
			fmt.Println("Admin password reset to the value of TELLER_ADMIN_PASSWORD.")
		}
		return
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		log.Fatalf("check users: %v", err)
	}
	if count == 0 {
		generated := os.Getenv("TELLER_ADMIN_PASSWORD") == ""
		pw, err := chooseAdminPassword()
		if err != nil {
			log.Fatal(err)
		}
		if _, err := a.bootstrapAdmin(pw, false); err != nil {
			log.Fatalf("create admin: %v", err)
		}
		fmt.Println("First run: admin account created.")
		fmt.Printf("  Email:    %s\n", adminEmail)
		if generated {
			path := credentialFilePath(dir)
			if err := writeCredentialFile(path, pw); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("  Password: written to %s\n", path)
			fmt.Println("  Sign in, then delete that file.")
		} else {
			fmt.Println("  Password: taken from TELLER_ADMIN_PASSWORD")
		}
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen on %s: %v (is another copy of teller running?)", *addr, err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	url := "http://localhost:" + port

	srv := &http.Server{Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("Teller running at %s (data: %s)", url, dir)
	if !*noBrowser {
		openBrowser(url)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	case <-stop:
		log.Println("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// chooseAdminPassword uses TELLER_ADMIN_PASSWORD when set, otherwise generates one.
func chooseAdminPassword() (string, error) {
	if pw := os.Getenv("TELLER_ADMIN_PASSWORD"); pw != "" {
		return pw, nil
	}
	return generatePassword()
}

func resolveDataDir(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv("TELLER_DATA"); env != "" {
		return env
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return "data"
}

func loadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, os.WriteFile(path, key, 0o600)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("could not open browser: %v (open %s manually)", err, url)
	}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /v1/auth/login", a.handle(a.login))
	mux.HandleFunc("POST /v1/auth/logout", a.protect(a.logout))
	mux.HandleFunc("POST /v1/auth/refresh", a.protect(a.refresh))
	mux.HandleFunc("POST /v1/auth/password-reset", a.handle(a.passwordReset))
	mux.HandleFunc("POST /v1/auth/change-password", a.protectPending(a.changePassword))

	mux.HandleFunc("GET /v1/users", a.requireRole([]string{roleAdmin}, a.listUsers))
	mux.HandleFunc("POST /v1/users", a.requireRole([]string{roleAdmin}, a.createUser))
	mux.HandleFunc("PATCH /v1/users/{id}", a.requireRole([]string{roleAdmin}, a.updateUser))
	mux.HandleFunc("POST /v1/users/{id}/reset-password", a.requireRole([]string{roleAdmin}, a.resetUserPassword))

	mux.HandleFunc("GET /v1/contacts", a.protect(a.listContacts))
	mux.HandleFunc("POST /v1/contacts", a.protect(a.createContact))
	mux.HandleFunc("GET /v1/contacts/{id}", a.protect(a.getContactDetail))
	mux.HandleFunc("PATCH /v1/contacts/{id}", a.protect(a.updateContact))
	mux.HandleFunc("DELETE /v1/contacts/{id}", a.protect(a.deleteContact))
	mux.HandleFunc("GET /v1/contacts/{contactId}/notes", a.protect(a.listContactNotes))
	mux.HandleFunc("POST /v1/contacts/{contactId}/notes", a.protect(a.createContactNote))

	mux.HandleFunc("GET /v1/deals", a.protect(a.listDeals))
	mux.HandleFunc("POST /v1/deals", a.protect(a.createDeal))
	mux.HandleFunc("GET /v1/deals/{id}", a.protect(a.getDealDetail))
	mux.HandleFunc("PATCH /v1/deals/{id}", a.protect(a.updateDeal))
	mux.HandleFunc("DELETE /v1/deals/{id}", a.protect(a.deleteDeal))

	mux.HandleFunc("GET /v1/tasks", a.protect(a.listTasks))
	mux.HandleFunc("POST /v1/tasks", a.protect(a.createTask))
	mux.HandleFunc("GET /v1/tasks/{id}", a.protect(a.getTaskHandler))
	mux.HandleFunc("PATCH /v1/tasks/{id}", a.protect(a.updateTask))
	mux.HandleFunc("DELETE /v1/tasks/{id}", a.protect(a.deleteTask))

	mux.HandleFunc("GET /v1/activities", a.protect(a.listActivitiesHandler))
	mux.HandleFunc("GET /v1/activities/{id}", a.protect(a.getActivityHandler))

	mux.HandleFunc("PATCH /v1/notes/{id}", a.protect(a.updateNote))
	mux.HandleFunc("DELETE /v1/notes/{id}", a.protect(a.deleteNote))

	mux.HandleFunc("GET /v1/dashboard/metrics", a.protect(a.dashboardMetrics))
	mux.HandleFunc("GET /v1/dashboard/my-tasks", a.protect(a.myTasks))

	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, &apiError{status: http.StatusNotFound, code: "NOT_FOUND", message: "No such endpoint"})
	})
	mux.Handle("/", a.spaHandler())
	return mux
}
