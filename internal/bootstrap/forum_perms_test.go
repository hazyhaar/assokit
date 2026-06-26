// CLAUDE:SUMMARY Test : BootstrapForumPerms pose node-forum + roles + node_permissions write (admin/member/moderator), idempotent.
package bootstrap

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openForumPermsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Sous-ensemble du schéma touché par BootstrapForumPerms (cf. internal/chassis/initial.sql).
	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id TEXT PRIMARY KEY, slug TEXT NOT NULL UNIQUE,
			type TEXT NOT NULL CHECK(type IN ('folder','page','post','form','doc')),
			title TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE roles (id TEXT PRIMARY KEY, label TEXT NOT NULL);
		CREATE TABLE node_permissions (
			node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
			role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
			permission TEXT NOT NULL CHECK(permission IN ('none','read','write','moderate','admin')),
			PRIMARY KEY(node_id, role_id)
		);
	`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestBootstrapForumPerms_GrantsWrite(t *testing.T) {
	db := openForumPermsTestDB(t)
	defer db.Close()

	if err := BootstrapForumPerms(db); err != nil {
		t.Fatalf("BootstrapForumPerms: %v", err)
	}

	var nodeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE id='node-forum'`).Scan(&nodeCount); err != nil || nodeCount != 1 {
		t.Fatalf("node-forum anchor: count=%d err=%v", nodeCount, err)
	}

	for _, role := range []string{"admin", "member", "moderator"} {
		var perm string
		err := db.QueryRow(
			`SELECT permission FROM node_permissions WHERE node_id='node-forum' AND role_id=?`, role,
		).Scan(&perm)
		if err != nil {
			t.Fatalf("perm role %s manquante: %v", role, err)
		}
		if perm != "write" {
			t.Errorf("perm role %s = %q, want write", role, perm)
		}
	}
}

func TestBootstrapForumPerms_Idempotent(t *testing.T) {
	db := openForumPermsTestDB(t)
	defer db.Close()

	for i := 0; i < 3; i++ {
		if err := BootstrapForumPerms(db); err != nil {
			t.Fatalf("BootstrapForumPerms run %d: %v", i, err)
		}
	}

	// Vérifie la PRÉSENCE des 3 rôles attendus EXACTEMENT — pas un simple
	// comptage opaque : on lit la liste des role_id distincts pour qu'un doublon
	// masqué (même count, rôle dupliqué/manquant) fasse échouer le test.
	rows, err := db.Query(
		`SELECT DISTINCT role_id FROM node_permissions WHERE node_id='node-forum' AND permission='write' ORDER BY role_id`,
	)
	if err != nil {
		t.Fatalf("query roles: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatalf("scan role: %v", err)
		}
		got = append(got, role)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	want := []string{"admin", "member", "moderator"}
	if len(got) != len(want) {
		t.Fatalf("rôles write sur node-forum après 3 runs = %v, want %v (exactement, sans doublon)", got, want)
	}
	for i, role := range want {
		if got[i] != role {
			t.Fatalf("rôles write sur node-forum = %v, want %v (présence exacte des 3 rôles)", got, want)
		}
	}

	// Le total de rows doit égaler le nombre de rôles distincts : aucune row
	// dupliquée n'a échappé au DISTINCT (garde anti-doublon sur 3 runs idempotents).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_permissions WHERE node_id='node-forum'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != len(want) {
		t.Errorf("node_permissions count après 3 runs = %d, want %d (admin, member, moderator, pas de doublon)", n, len(want))
	}
}
