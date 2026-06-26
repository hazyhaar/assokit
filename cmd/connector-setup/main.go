// connector-setup — outil d'ops (pôle-only, NON exporté) qui configure un
// connecteur assokit headless sur une instance déployée, sans passer par le flux
// admin HTTP (magic-link + CSRF). Il réplique exactement AdminConnectorConfigure :
// insère la ligne parente `connectors` (enabled=1), puis chiffre les secrets au
// Vault via la même API assets.Vault.Set (AES-256-GCM, clé ASSOKIT_MASTER_KEY).
//
// Conçu pour être piloté par le runbook horos55 `assokit_livekit_connector` :
// build linux -> scp -> exécution sur le serveur avec l'environnement réel du
// service (ASSOKIT_MASTER_KEY + DB_PATH), paramètres du connecteur passés en
// variables d'environnement (jamais en argv, pour ne pas exposer un secret en
// liste de processus).
//
// Idempotent : ON CONFLICT met à jour. Rejouer = sûr.
//
// Usage :
//
//	ASSOKIT_MASTER_KEY=<hex64> CONNECTOR_ID=livekit \
//	  SECRET_server_url=wss://... SECRET_api_key=... SECRET_api_secret=... \
//	  connector-setup -db /opt/assokit-v2/data/assokit.db
//
// Toute variable d'environnement préfixée `SECRET_` est stockée chiffrée au Vault
// sous la clé correspondante (le préfixe retiré). Les champs FK vers users(id)
// (connectors.configured_by, connector_credentials.set_by) sont laissés NULL :
// aucun utilisateur "cli" n'existe.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/hazyhaar/assokit/pkg/connectors/assets"
	_ "modernc.org/sqlite"
)

func main() {
	dbPath := flag.String("db", os.Getenv("DB_PATH"), "chemin de la DB assokit (sinon $DB_PATH)")
	connectorID := flag.String("id", os.Getenv("CONNECTOR_ID"), "id du connecteur (sinon $CONNECTOR_ID)")
	flag.Parse()

	masterKey := os.Getenv("ASSOKIT_MASTER_KEY")
	secrets := collectSecrets() // SECRET_<key>=<value>

	switch {
	case *dbPath == "":
		fail("manque -db ou $DB_PATH")
	case masterKey == "":
		fail("manque $ASSOKIT_MASTER_KEY")
	case *connectorID == "":
		fail("manque -id ou $CONNECTOR_ID")
	case len(secrets) == 0:
		fail("aucun secret SECRET_<key>=<value> dans l'environnement")
	}

	db, err := sql.Open("sqlite", *dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	must(err)
	db.SetMaxOpenConns(1) // pragma FK cohérent sur l'unique connexion
	defer db.Close()

	keys, err := applyConnectorConfig(context.Background(), db, masterKey, *connectorID, secrets)
	must(err)
	fmt.Printf("connecteur %s configuré (enabled=1, %d secrets au Vault : %s)\n",
		*connectorID, len(keys), strings.Join(keys, ", "))
}

// applyConnectorConfig insère la ligne parente connectors (enabled=1, FK users
// laissées NULL) puis chiffre chaque secret au Vault. Idempotent (ON CONFLICT).
// Retourne les clés écrites, triées. Cœur testable de l'outil.
func applyConnectorConfig(ctx context.Context, db *sql.DB, masterKey, connectorID string, secrets map[string]string) ([]string, error) {
	// 1. Ligne parente connectors d'abord (satisfait la FK de connector_credentials).
	//    configured_by NULL : pas d'utilisateur "cli" dans users(id).
	if _, err := db.ExecContext(ctx, `
		INSERT INTO connectors(id, enabled, config_json, configured_at, configured_by)
		VALUES (?, 1, '{}', CURRENT_TIMESTAMP, NULL)
		ON CONFLICT(id) DO UPDATE SET enabled = 1, configured_at = CURRENT_TIMESTAMP`,
		connectorID); err != nil {
		return nil, fmt.Errorf("insert connectors: %w", err)
	}
	// 2. Secrets chiffrés au Vault (byActor vide => set_by NULL, FK users respectée).
	v, err := assets.NewVault(db, masterKey)
	if err != nil {
		return nil, fmt.Errorf("NewVault: %w", err)
	}
	keys := make([]string, 0, len(secrets))
	for k := range secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys) // ordre déterministe
	for _, k := range keys {
		if err := v.Set(ctx, connectorID, k, secrets[k], ""); err != nil {
			return nil, fmt.Errorf("Vault.Set %s/%s: %w", connectorID, k, err)
		}
	}
	return keys, nil
}

// collectSecrets lit toute variable d'environnement préfixée SECRET_ et retourne
// une map clé->valeur (préfixe retiré).
func collectSecrets() map[string]string {
	const prefix = "SECRET_"
	out := map[string]string{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 || !strings.HasPrefix(kv, prefix) {
			continue
		}
		name := kv[len(prefix):eq]
		if name == "" {
			continue
		}
		out[name] = kv[eq+1:]
	}
	return out
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "connector-setup: "+msg)
	os.Exit(1)
}
