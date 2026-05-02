// CLAUDE:SUMMARY Tests op.Storage OAuth SQLite : authcode flow, refresh rotation, scopes RBAC, revoke (M-ASSOKIT-OAUTH-1).
package oauth_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/hazyhaar/assokit/internal/chassis"
	"github.com/hazyhaar/assokit/internal/oauth"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

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

	// Vérifier que le tokenID (opaque bearer) est retrouvable via son hash SHA256
	tokenHash := sha256Hex(tokenID)
	var bearerUID string
	err = db.QueryRowContext(ctx,
		`SELECT user_id FROM oauth_tokens WHERE access_token_hash=? AND revoked_at IS NULL AND expires_at > ?`,
		tokenHash, time.Now().UTC().Format(time.RFC3339),
	).Scan(&bearerUID)
	if err != nil {
		t.Fatalf("Bearer lookup (MCP endpoint) : token non trouvable en DB: %v", err)
	}
	if bearerUID != userID {
		t.Errorf("Bearer lookup : want userID=%s, got %s", userID, bearerUID)
	}

	// Vérifier que SetUserinfoFromToken retourne les claims corrects
	userinfo := new(oidc.UserInfo)
	if err := store.SetUserinfoFromToken(ctx, userinfo, tokenID, userID, ""); err != nil {
		t.Fatalf("SetUserinfoFromToken (access token utilisable) : %v", err)
	}
	if userinfo.Subject != userID {
		t.Errorf("userinfo.Subject : want %s, got %s", userID, userinfo.Subject)
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

// TestOAuth_AuthCodeReplayRefused : le même auth code ne peut pas être échangé deux fois.
func TestOAuth_AuthCodeReplayRefused(t *testing.T) {
	store, db := newTestStorage(t)
	ctx := context.Background()
	userID, clientID, _ := seedUserAndClient(t, db)

	oidcReq := &oidc.AuthRequest{
		ClientID:     clientID,
		RedirectURI:  "http://localhost:8080/callback",
		ResponseType: oidc.ResponseTypeCode,
		Scopes:       oidc.SpaceDelimitedArray{"openid"},
	}
	ar, err := store.CreateAuthRequest(ctx, oidcReq, "")
	if err != nil {
		t.Fatalf("CreateAuthRequest: %v", err)
	}
	store.CompleteAuthRequest(ctx, ar.GetID(), userID) //nolint:errcheck

	code := "replay-test-code-abc"
	if err := store.SaveAuthCode(ctx, ar.GetID(), code); err != nil {
		t.Fatalf("SaveAuthCode: %v", err)
	}

	// Premier échange : doit réussir
	ar2, err := store.AuthRequestByCode(ctx, code)
	if err != nil {
		t.Fatalf("AuthRequestByCode (1er échange) : %v", err)
	}
	_ = ar2

	// Simuler le handler token qui appelle DeleteAuthRequest pour invalider le code
	if err := store.DeleteAuthRequest(ctx, ar.GetID()); err != nil {
		t.Fatalf("DeleteAuthRequest : %v", err)
	}

	// Deuxième échange avec le même code → doit échouer
	_, err = store.AuthRequestByCode(ctx, code)
	if err == nil {
		t.Error("replay : 2ème échange du même code devrait retourner une erreur")
	}
}

// TestOAuth_RevokeAccessTokenAlsoRevokesRefresh : révoquer l'access token révoque aussi le refresh associé.
func TestOAuth_RevokeAccessTokenAlsoRevokesRefresh(t *testing.T) {
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

	at, rt, _, err := store.CreateAccessAndRefreshTokens(ctx, ar, "")
	if err != nil {
		t.Fatalf("CreateAccessAndRefreshTokens: %v", err)
	}

	// Vérifier que le refresh token est valide avant révocation
	_, err = store.TokenRequestByRefreshToken(ctx, rt)
	if err != nil {
		t.Fatalf("précondition : refresh token doit être valide: %v", err)
	}

	// Révoquer l'access token (by access_token_hash lookup)
	if oidcErr := store.RevokeToken(ctx, at, userID, clientID); oidcErr != nil {
		t.Fatalf("RevokeToken(access): %v", oidcErr)
	}

	// Le refresh token de la même row doit être révoqué (même row dans oauth_tokens)
	_, err = store.TokenRequestByRefreshToken(ctx, rt)
	if err == nil {
		t.Error("après révocation de l'access token, le refresh token associé devrait être invalide")
	}

	// Vérification directe en DB via revoked_at count
	var activeCount int
	dbErr := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM oauth_tokens WHERE revoked_at IS NULL`,
	).Scan(&activeCount)
	if dbErr != nil {
		t.Fatalf("DB count active tokens: %v", dbErr)
	}
	if activeCount != 0 {
		t.Errorf("après révocation de l'access token, aucun token actif ne devrait rester, got %d", activeCount)
	}
}
