package bootstrap

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// bootstrapAdmin crée le user admin si la table users est vide.
// Idempotent : ne s'exécute que si aucun user n'existe.
// Si adminPassword est vide, génère un password random 16 chars et le logue.
func bootstrapAdmin(db *sql.DB, adminEmail, adminPassword string, logger *slog.Logger) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("bootstrapAdmin count: %w", err)
	}
	if count > 0 {
		return nil // admin déjà présent
	}

	generated := adminPassword == ""
	if generated {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return fmt.Errorf("bootstrapAdmin rand: %w", err)
		}
		adminPassword = hex.EncodeToString(b)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), 12)
	if err != nil {
		return fmt.Errorf("bootstrapAdmin bcrypt: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().UTC().Format("2006-01-02 15:04:05")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("bootstrapAdmin begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	_, err = tx.Exec(
		`INSERT INTO users(id, email, password_hash, display_name, created_at) VALUES(?,?,?,?,?)`,
		id, adminEmail, string(hash), "Admin", now,
	)
	if err != nil {
		return fmt.Errorf("bootstrapAdmin insert user: %w", err)
	}

	_, err = tx.Exec(`INSERT INTO user_roles(user_id, role_id) VALUES(?,?)`, id, "admin")
	if err != nil {
		return fmt.Errorf("bootstrapAdmin insert role: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("bootstrapAdmin commit: %w", err)
	}

	// Vérifie que l'insert s'est bien passé
	var check int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE email=?`, adminEmail).Scan(&check); err != nil || check == 0 {
		return fmt.Errorf("bootstrapAdmin: user non créé après insert")
	}

	if generated {
		logger.Warn("ADMIN CRÉÉ — CHANGER LE MOT DE PASSE IMMÉDIATEMENT",
			"email", adminEmail,
			"password_initial", adminPassword,
		)
	} else {
		logger.Info("admin bootstrapped", "email", adminEmail)
	}
	return nil
}
