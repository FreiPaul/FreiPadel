package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const cliUsage = `FreiPadel admin CLI (runs against DATA_DIR, also works while the server is running)

Usage:
  freipadel list-users                              show all accounts
  freipadel reset-password <email> <new-password>   set a new password (and log the user out everywhere)
  freipadel promote-admin <email>                   grant admin rights
`

// runCLI handles maintenance commands like password resets. It opens the
// same database as the server; WAL mode + busy_timeout make concurrent
// access from a docker exec safe.
func runCLI(args []string) {
	dataDir := envOr("DATA_DIR", "./data")
	db, err := openDB(filepath.Join(dataDir, "freipadel.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	switch args[0] {
	case "list-users":
		cliListUsers(db)
	case "reset-password":
		if len(args) != 3 {
			fmt.Fprint(os.Stderr, cliUsage)
			os.Exit(2)
		}
		cliResetPassword(db, args[1], args[2])
	case "promote-admin":
		if len(args) != 2 {
			fmt.Fprint(os.Stderr, cliUsage)
			os.Exit(2)
		}
		cliPromoteAdmin(db, args[1])
	default:
		fmt.Fprint(os.Stderr, cliUsage)
		os.Exit(2)
	}
}

func cliListUsers(db *sql.DB) {
	rows, err := db.Query(`SELECT id, email, name, is_admin, created_at FROM users ORDER BY id`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	fmt.Printf("%-4s %-35s %-20s %-6s %s\n", "ID", "EMAIL", "NAME", "ADMIN", "CREATED")
	for rows.Next() {
		var id int64
		var email, name, created string
		var isAdmin int
		if err := rows.Scan(&id, &email, &name, &isAdmin, &created); err != nil {
			log.Fatal(err)
		}
		admin := ""
		if isAdmin == 1 {
			admin = "yes"
		}
		fmt.Printf("%-4d %-35s %-20s %-6s %s\n", id, email, name, admin, created)
	}
}

func cliResetPassword(db *sql.DB, email, password string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(password) < 8 {
		log.Fatal("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	res, err := db.Exec(`UPDATE users SET password_hash = ? WHERE email = ?`, string(hash), email)
	if err != nil {
		log.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Fatalf("no account with email %q — try `freipadel list-users`", email)
	}
	// Invalidate existing sessions so only the new password works.
	_, _ = db.Exec(`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE email = ?)`, email)
	fmt.Printf("password reset for %s — all their sessions were logged out\n", email)
}

func cliPromoteAdmin(db *sql.DB, email string) {
	email = strings.ToLower(strings.TrimSpace(email))
	res, err := db.Exec(`UPDATE users SET is_admin = 1 WHERE email = ?`, email)
	if err != nil {
		log.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		log.Fatalf("no account with email %q — try `freipadel list-users`", email)
	}
	fmt.Printf("%s is now an admin\n", email)
}
