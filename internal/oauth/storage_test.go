// CLAUDE:SUMMARY Tests op.Storage OAuth SQLite : authcode flow, refresh rotation, scopes RBAC, revoke (M-ASSOKIT-OAUTH-1).
package oauth_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var testSigningKey = []byte("test-signing-key-32-bytes-abcdefg")

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := chassis.Run(db); err != nil {
		t.Fatalf("chassis.Run: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestStorage(t *testing.T) (*oauth.Storage, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	rbacStore := &rbac.Store{DB: db}
	return oauth.New(db, testSigningKey, rbacStore), db
}

// seedUser insère un utilisateur et un client OAuth dans la DB de test.
func seedUserAndClient(t *testing.T, db *sql.DB) (userID, clientID, clientSecret string) {
	t.Helper()
	userID = "test-user-1"
	clientID = "test-client-1"
	clientSecret = "test-secret-abc"

	db.Exec(`INSERT INTO users(id, email, password_hash, display_name, is_active, created_at) VALUES(?,?,?,?,1,?)`,
		userID, "test@nps.fr", "hashed", "Test User", time.Now().UTC().Format(time.RFC3339))

	store := oauth.New(db, testSigningKey, &rbac.Store{DB: db})
	store.CreateClient(context.Background(), clientID, clientSecret,
		[]string{"http://localhost:8080/callback"},
		[]string{string(oidc.GrantTypeCode), string(oidc.GrantTypeRefreshToken)},
		[]string{"openid", "email", "profile", "offline_access"},
	)
	return
}

// TestOAuth_AuthCodeFlowFullJourney : create client → CreateAuthRequest → CompleteAuthRequest → SaveAuthCode → AuthRequestByCode.
func TestOAuth_AuthCodeFlowFullJourney(t *testing.T) {
	store, db := newTestStorage(t)
	ctx := context.Background()
	userID, clientID, _ := seedUserAndClient(t, db)

	oidcReq := &oidc.AuthRequest{
		ClientID:     clientID,
		RedirectURI:  "http://localhost:8080/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid", "email"},
		Nonce:        "nonce-abc",
		State:        "state-xyz",
	}

	ar, err := store.CreateAuthRequest(ctx, oidcReq, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}
	if ar.GetClientID() != clientID {
		t.Errorf("clientID: want %s, got %s", clientID, ar.GetClientID())
	}
	if ar.Done() {
		t.Error("auth request should not be done before CompleteAuthRequest")
	}

	// Compléter avec le userID après consent
	if err := store.CompleteAuthRequest(ctx, ar.GetID(), userID); err != nil {
		t.Fatalf("CompleteAuthRequest: %v", err)
	}

	ar2, err := store.AuthRequestByID(ctx, ar.GetID())
	if err != nil {
		t.Fatalf("AuthRequestByID: %v", err)
	}
	if !ar2.Done() {
		t.Error("auth request should be done after CompleteAuthRequest")
	}
	if ar2.GetSubject() != userID {
		t.Errorf("subject: want %s, got %s", userID, ar2.GetSubject())
	}

	// Sauvegarder le code
	code := "authcode-test-123"
	if err := store.SaveAuthCode(ctx, ar.GetID(), code); err != nil {
		t.Fatalf("SaveAuthCode: %v", err)
	}

	// Récupérer par code
	ar3, err := store.AuthRequestByCode(ctx, code)
	if err != nil {
		t.Fatalf("AuthRequestByCode: %v", err)
	}
	if ar3.GetClientID() != clientID {
		t.Errorf("AuthRequestByCode clientID: want %s, got %s", clientID, ar3.GetClientID())
	}

	// Créer access token
	tokenID, exp, err := store.CreateAccessToken(ctx, ar3)
	if err != nil {
		t.Fatalf("CreateAccessToken: %v", err)
	}
	if tokenID == "" {
		t.Error("tokenID vide")
	}
	if exp.Before(time.Now()) {
		t.Error("token déjà expiré")
	}
}

// TestOAuth_RefreshTokenRotationInvalidatesPrevious : rotation invalide le refresh token précédent.
func TestOAuth_RefreshTokenRotationInvalidatesPrevious(t *testing.T) {
	store, db := newTestStorage(t)
	ctx := context.Background()
	userID, clientID, _ := seedUserAndClient(t, db)

	oidcReq := &oidc.AuthRequest{
		ClientID:     clientID,
		RedirectURI:  "http://localhost:8080/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid", "offline_access"},
	}
	ar, _ := store.CreateAuthRequest(ctx, oidcReq, "")
	store.CompleteAuthRequest(ctx, ar.GetID(), userID) //nolint:errcheck

	// Premier pair de tokens
	_, rt1, _, err := store.CreateAccessAndRefreshTokens(ctx, ar, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}

	// Rotation : nouvel access token via refresh token 1
	refreshReq, err := store.TokenRequestByRefreshToken(ctx, rt1)
	if err != nil {
		t.Fatalf("TokenRequestByRefreshToken: %v", err)
	}
	_, rt2, _, err := store.CreateAccessAndRefreshTokens(ctx, refreshReq, rt1)
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens rotation: %v", err)
	}

	// rt1 doit être invalide maintenant
	_, err = store.TokenRequestByRefreshToken(ctx, rt1)
	if err == nil {
		t.Error("rt1 devrait être invalidé après rotation")
	}

	// rt2 doit être valide
	_, err = store.TokenRequestByRefreshToken(ctx, rt2)
	if err != nil {
		t.Errorf("rt2 devrait être valide, got: %v", err)
	}
}

// TestOAuth_ScopesIntersectWithRBACPermissions : le token n'inclut que les perms réelles de l'user.
func TestOAuth_ScopesIntersectWithRBACPermissions(t *testing.T) {
	store, db := newTestStorage(t)
	ctx := context.Background()
	userID, clientID, _ := seedUserAndClient(t, db)

	// Donner à l'user une seule permission RBAC
	rbacStore := &rbac.Store{DB: db}
	svc := &rbac.Service{Store: rbacStore, Cache: &rbac.Cache{}}
	gID, _ := svc.Store.CreateGrade(ctx, "test-grade-scopes")
	pID, _ := svc.Store.EnsurePermission(ctx, "feedback.triage", "")
	svc.GrantPerm(ctx, gID, pID)        //nolint:errcheck
	svc.AssignGrade(ctx, userID, gID)   //nolint:errcheck
	svc.Recompute(ctx, userID)          //nolint:errcheck

	// Créer token avec scopes qui incluent des perms demandées
	oidcReq := &oidc.AuthRequest{
		ClientID:     clientID,
		RedirectURI:  "http://localhost:8080/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid", "feedback.triage", "forum.delete"},
	}
	ar, _ := store.CreateAuthRequest(ctx, oidcReq, "")
	store.CompleteAuthRequest(ctx, ar.GetID(), userID) //nolint:errcheck

	// Simuler le flow réel : SaveAuthCode → AuthRequestByCode → CreateAccessToken
	code := "scope-test-code"
	store.SaveAuthCode(ctx, ar.GetID(), code) //nolint:errcheck
	freshAR, err := store.AuthRequestByCode(ctx, code)
	if err != nil {
		t.Fatalf("AuthRequestByCode: %v", err)
	}

	tokenID, _, err := store.CreateAccessToken(ctx, freshAR)
	if err != nil {
		t.Fatalf("CreateAccessToken: %v", err)
	}

	// Remplir userinfo depuis token
	userinfo := new(oidc.UserInfo)
	if err := store.SetUserinfoFromToken(ctx, userinfo, tokenID, userID, ""); err != nil {
		t.Fatalf("SetUserinfoFromToken: %v", err)
	}

	// Le claim "permissions" doit contenir feedback.triage mais pas forum.delete
	permsRaw := userinfo.Claims["permissions"]
	if permsRaw == nil {
		t.Fatal("claim permissions absent")
	}
	perms, ok := permsRaw.([]string)
	if !ok {
		t.Fatalf("permissions n'est pas []string: %T", permsRaw)
	}
	hasTriage := false
	hasForumDelete := false
	for _, p := range perms {
		if p == "feedback.triage" {
			hasTriage = true
		}
		if p == "forum.delete" {
			hasForumDelete = true
		}
	}
	if !hasTriage {
		t.Error("feedback.triage devrait être dans les permissions")
	}
	if hasForumDelete {
		t.Error("forum.delete ne devrait pas être dans les permissions (user ne l'a pas)")
	}
}

// TestOAuth_RevokeImmediatelyInvalidates : RevokeToken invalide immédiatement le refresh token.
func TestOAuth_RevokeImmediatelyInvalidates(t *testing.T) {
	store, db := newTestStorage(t)
	ctx := context.Background()
	userID, clientID, _ := seedUserAndClient(t, db)

	oidcReq := &oidc.AuthRequest{
		ClientID:     clientID,
		RedirectURI:  "http://localhost:8080/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid", "offline_access"},
	}
	ar, _ := store.CreateAuthRequest(ctx, oidcReq, "")
	store.CompleteAuthRequest(ctx, ar.GetID(), userID) //nolint:errcheck

	_, rt, _, err := store.CreateAccessAndRefreshTokens(ctx, ar, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}

	// Révoquer le refresh token
	if oidcErr := store.RevokeToken(ctx, rt, "", clientID); oidcErr != nil {
		t.Fatalf("RevokeToken: %v", oidcErr)
	}

	// Le refresh token doit être invalide
	_, err = store.TokenRequestByRefreshToken(ctx, rt)
	if err == nil {
		t.Error("refresh token devrait être invalide après révocation")
	}
}

// TestOAuth_StorageInterfaceCompiles : vérifie la satisfaction de l'interface au compile time.
func TestOAuth_StorageInterfaceCompiles(t *testing.T) {
	db := openTestDB(t)
	var _ op.Storage = oauth.New(db, testSigningKey, nil)
}
