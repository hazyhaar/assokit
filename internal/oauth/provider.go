// CLAUDE:SUMMARY Shim internal/oauth → pkg/oauth : adapte l'ancienne signature (rbac.Store) vers les interfaces UserProvider+IDGenerator (M-ASSOKIT-OAUTH-1).
// Ce paquet est conservé pour la rétro-compatibilité interne assokit. Les consommateurs externes utilisent pkg/oauth directement.
package oauth

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	pkgoauth "github.com/hazyhaar/assokit/pkg/oauth"
	"github.com/hazyhaar/assokit/pkg/rbac"
	"github.com/hazyhaar/assokit/pkg/uid"
)

// Storage est un alias du Storage public pour la rétro-compatibilité.
type Storage = pkgoauth.Storage

// uidGen implémente pkgoauth.IDGenerator via uid.New().
type uidGen struct{}

func (uidGen) NewID() string { return uid.New() }

// rbacUserProvider implémente pkgoauth.UserProvider sur la table users + user_effective_permissions.
type rbacUserProvider struct {
	db        *sql.DB
	rbacStore *rbac.Store
}

func (p *rbacUserProvider) GetUser(ctx context.Context, id string) (*pkgoauth.UserInfo, error) {
	row := p.db.QueryRowContext(ctx, `SELECT id, email, display_name FROM users WHERE id = ?`, id)
	var u pkgoauth.UserInfo
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("rbacUserProvider.GetUser: %w", err)
	}
	return &u, nil
}

func (p *rbacUserProvider) ListPermissions(ctx context.Context, userID string) ([]string, error) {
	if p.rbacStore == nil {
		return nil, nil
	}
	rows, err := p.db.QueryContext(ctx,
		`SELECT p.name FROM user_effective_permissions uep
		 JOIN permissions p ON p.id = uep.permission_id
		 WHERE uep.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var perm string
		rows.Scan(&perm) //nolint:errcheck
		perms = append(perms, perm)
	}
	return perms, rows.Err()
}

// New crée un Storage OAuth avec l'ancienne signature (rétro-compatibilité assokit).
// rbacStore peut être nil (permissions non injectées dans les claims).
func New(db *sql.DB, signingKeyBytes []byte, rbacStore *rbac.Store) *Storage {
	up := &rbacUserProvider{db: db, rbacStore: rbacStore}
	return pkgoauth.New(db, signingKeyBytes, up, uidGen{})
}

// NewProvider crée le Provider OIDC avec l'ancienne signature (rétro-compatibilité assokit).
// rbacStore peut être nil.
func NewProvider(db *sql.DB, issuer string, signingKey []byte, allowInsecure bool, rbacStore *rbac.Store) (http.Handler, *Storage, error) {
	up := &rbacUserProvider{db: db, rbacStore: rbacStore}
	return pkgoauth.NewProvider(db, issuer, signingKey, allowInsecure, up, uidGen{})
}
