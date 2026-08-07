package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"freipadel/internal/store"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
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
	storage, err := store.Open(filepath.Join(dataDir, "freipadel.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer storage.Close()
	db := storage.ORM

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

func cliListUsers(db *gorm.DB) {
	users, err := store.ListUsers(db)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%-4s %-35s %-20s %-6s %s\n", "ID", "EMAIL", "NAME", "ADMIN", "CREATED")
	for _, user := range users {
		admin := ""
		if user.IsAdmin {
			admin = "yes"
		}
		fmt.Printf("%-4d %-35s %-20s %-6s %s\n", user.ID, user.Email, user.Name, admin, user.CreatedAt)
	}
}

func cliResetPassword(db *gorm.DB, email, password string) {
	email = strings.ToLower(strings.TrimSpace(email))
	if len(password) < 8 {
		log.Fatal("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatal(err)
	}
	affected, err := store.UpdatePassword(db, email, string(hash))
	if err != nil {
		log.Fatal(err)
	}
	if affected == 0 {
		log.Fatalf("no account with email %q — try `freipadel list-users`", email)
	}
	// Invalidate existing sessions so only the new password works.
	_ = store.DeleteSessionsForUserEmail(db, email)
	fmt.Printf("password reset for %s — all their sessions were logged out\n", email)
}

func cliPromoteAdmin(db *gorm.DB, email string) {
	email = strings.ToLower(strings.TrimSpace(email))
	affected, err := store.PromoteAdmin(db, email)
	if err != nil {
		log.Fatal(err)
	}
	if affected == 0 {
		log.Fatalf("no account with email %q — try `freipadel list-users`", email)
	}
	fmt.Printf("%s is now an admin\n", email)
}
