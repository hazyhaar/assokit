package api

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
)

// openDB ouvre la base SQLite en appliquant les pragmas indispensables au
// runtime via le DSN modernc : foreign_keys(1) (sans quoi toutes les FK et
// ON DELETE CASCADE du schéma sont inertes), journal_mode(WAL),
// busy_timeout(10000), synchronous(NORMAL). SetMaxOpenConns(1) garantit le
// single-writer SQLite et préserve une DB ":memory:" sur la durée de vie.
//
// Helper autonome (pas dbopen horos55) : assokit reste import-clean côté
// persistance pour rester publiable.
func openDB(path string) (*sql.DB, error) {
	pragmas := url.Values{}
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "journal_mode(WAL)")
	pragmas.Add("_pragma", "busy_timeout(10000)")
	pragmas.Add("_pragma", "synchronous(NORMAL)")

	// modernc exige le préfixe file: pour accepter une query de pragmas ;
	// ":memory:" devient "file::memory:".
	var dsn string
	switch {
	case path == ":memory:":
		dsn = "file::memory:?" + pragmas.Encode()
	case strings.HasPrefix(path, "file:"):
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		dsn = path + sep + pragmas.Encode()
	default:
		dsn = "file:" + path + "?" + pragmas.Encode()
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("openDB: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single-writer + persistance ":memory:"
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("openDB: ping: %w", err)
	}
	return db, nil
}
