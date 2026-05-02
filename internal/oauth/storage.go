// CLAUDE:SUMMARY OAuth 2.1 SQLite storage : implémente op.Storage (AuthStorage+OPStorage) — authcodes, tokens HS256, clients, userinfo (M-ASSOKIT-OAUTH-1).
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/hazyhaar/assokit/pkg/horui/rbac"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

var (
	_ op.Storage = (*Storage)(nil)

	ErrNotFound     = errors.New("oauth: not found")
	ErrCodeUsed     = errors.New("oauth: auth code already used")
	ErrTokenRevoked = errors.New("oauth: token revoked")
)

// Storage implémente op.Storage avec SQLite.
type Storage struct {
	db         *sql.DB
	signingKey *hmacKey
	rbacStore  *rbac.Store
}

// New crée un Storage OAuth avec la clé HS256 fournie (32+ bytes recommandés).
func New(db *sql.DB, signingKeyBytes []byte, rbacStore *rbac.Store) *Storage {
	sum := sha256.Sum256(signingKeyBytes)
	return &Storage{
		db:        db,
		rbacStore: rbacStore,
		signingKey: &hmacKey{
			id:  "hs256-1",
			key: sum[:],
		},
	}
}

// ─── signing ─────────────────────────────────────────────────────────────────

type hmacKey struct {
	id  string
	key []byte
}

func (k *hmacKey) ID() string                            { return k.id }
func (k *hmacKey) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.HS256 }
func (k *hmacKey) Key() any                              { return k.key }

func (s *Storage) SigningKey(_ context.Context) (op.SigningKey, error) {
	return s.signingKey, nil
}

func (s *Storage) SignatureAlgorithms(_ context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.HS256}, nil
}

// KeySet retourne vide — les clés symétriques HS256 ne sont pas publiées dans JWKS.
func (s *Storage) KeySet(_ context.Context) ([]op.Key, error) {
	return nil, nil
}

// ─── health ──────────────────────────────────────────────────────────────────

func (s *Storage) Health(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// ─── clients ─────────────────────────────────────────────────────────────────

type oauthClient struct {
	id           string
	secretHash   string
	redirectURIs []string
	grantTypes   []oidc.GrantType
	scopes       []string
}

func (c *oauthClient) GetID() string                      { return c.id }
func (c *oauthClient) RedirectURIs() []string             { return c.redirectURIs }
func (c *oauthClient) PostLogoutRedirectURIs() []string   { return nil }
func (c *oauthClient) ApplicationType() op.ApplicationType { return op.ApplicationTypeWeb }
func (c *oauthClient) AuthMethod() oidc.AuthMethod        { return oidc.AuthMethodBasic }
func (c *oauthClient) ResponseTypes() []oidc.ResponseType { return []oidc.ResponseType{oidc.ResponseTypeCode} }
func (c *oauthClient) GrantTypes() []oidc.GrantType       { return c.grantTypes }
func (c *oauthClient) LoginURL(id string) string          { return "/oauth2/consent?id=" + id }
func (c *oauthClient) AccessTokenType() op.AccessTokenType { return op.AccessTokenTypeJWT }
func (c *oauthClient) IDTokenLifetime() time.Duration     { return time.Hour }
func (c *oauthClient) DevMode() bool                      { return false }
func (c *oauthClient) RestrictAdditionalIdTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *oauthClient) RestrictAdditionalAccessTokenScopes() func([]string) []string {
	return func(s []string) []string { return s }
}
func (c *oauthClient) IsScopeAllowed(scope string) bool {
	for _, s := range c.scopes {
		if s == scope {
			return true
		}
	}
	return scope == oidc.ScopeOpenID || scope == oidc.ScopeOfflineAccess || scope == oidc.ScopeProfile || scope == oidc.ScopeEmail
}
func (c *oauthClient) IDTokenUserinfoClaimsAssertion() bool { return false }
func (c *oauthClient) ClockSkew() time.Duration             { return 0 }

func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT client_id, client_secret_hash, redirect_uris, grant_types, scopes FROM oauth_clients WHERE client_id = ?`,
		clientID)
	var c oauthClient
	var rawRedirects, rawGrants, rawScopes string
	if err := row.Scan(&c.id, &c.secretHash, &rawRedirects, &rawGrants, &rawScopes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: client %s", ErrNotFound, clientID)
		}
		return nil, err
	}
	json.Unmarshal([]byte(rawRedirects), &c.redirectURIs)   //nolint:errcheck
	json.Unmarshal([]byte(rawGrants), &c.grantTypes)        //nolint:errcheck
	json.Unmarshal([]byte(rawScopes), &c.scopes)            //nolint:errcheck
	return &c, nil
}

func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT client_secret_hash FROM oauth_clients WHERE client_id = ?`, clientID).Scan(&hash)
	if err != nil {
		return fmt.Errorf("%w: client %s", ErrNotFound, clientID)
	}
	got := hashSecret(clientSecret)
	if got != hash {
		return errors.New("oauth: invalid client secret")
	}
	return nil
}

// ─── auth requests ────────────────────────────────────────────────────────────

type authRequest struct {
	id                  string
	clientID            string
	userID              string
	scopes              []string
	redirectURI         string
	nonce               string
	state               string
	codeChallenge       *oidc.CodeChallenge
	responseType        oidc.ResponseType
	authTime            time.Time
	done                bool
}

func (r *authRequest) GetID() string                          { return r.id }
func (r *authRequest) GetACR() string                         { return "" }
func (r *authRequest) GetAMR() []string                       { return nil }
func (r *authRequest) GetAudience() []string                  { return []string{r.clientID} }
func (r *authRequest) GetAuthTime() time.Time                 { return r.authTime }
func (r *authRequest) GetClientID() string                    { return r.clientID }
func (r *authRequest) GetCodeChallenge() *oidc.CodeChallenge  { return r.codeChallenge }
func (r *authRequest) GetNonce() string                       { return r.nonce }
func (r *authRequest) GetRedirectURI() string                 { return r.redirectURI }
func (r *authRequest) GetResponseType() oidc.ResponseType     { return r.responseType }
func (r *authRequest) GetResponseMode() oidc.ResponseMode     { return "" }
func (r *authRequest) GetScopes() []string                    { return r.scopes }
func (r *authRequest) GetState() string                       { return r.state }
func (r *authRequest) GetSubject() string                     { return r.userID }
func (r *authRequest) Done() bool                             { return r.done }

func (s *Storage) CreateAuthRequest(ctx context.Context, req *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	id := uuid.NewString()
	scopes, _ := json.Marshal(req.Scopes)
	var cc, ccMethod string
	if req.CodeChallenge != "" {
		cc = req.CodeChallenge
		ccMethod = string(req.CodeChallengeMethod)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_auth_requests(id, client_id, user_id, scopes, redirect_uri, nonce, state, code_challenge, code_challenge_method, response_type, auth_time)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, req.ClientID, userID,
		string(scopes), req.RedirectURI, req.Nonce, req.State,
		cc, ccMethod, string(req.ResponseType),
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return s.authRequestByID(ctx, id)
}

func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	return s.authRequestByID(ctx, id)
}

func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	var authReqID string
	err := s.db.QueryRowContext(ctx, `SELECT auth_req_id FROM oauth_authcodes WHERE code = ? AND used_at IS NULL AND expires_at > ?`,
		code, time.Now().UTC().Format(time.RFC3339)).Scan(&authReqID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrCodeUsed
		}
		return nil, err
	}
	return s.authRequestByID(ctx, authReqID)
}

func (s *Storage) authRequestByID(ctx context.Context, id string) (*authRequest, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, client_id, user_id, scopes, redirect_uri, nonce, state, code_challenge, code_challenge_method, response_type, auth_time, user_id != ''
		 FROM oauth_auth_requests WHERE id = ?`, id)
	var r authRequest
	var rawScopes, cc, ccMethod, authTimeStr string
	var isDone bool
	if err := row.Scan(&r.id, &r.clientID, &r.userID, &rawScopes, &r.redirectURI, &r.nonce, &r.state, &cc, &ccMethod, &r.responseType, &authTimeStr, &isDone); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: auth request %s", ErrNotFound, id)
		}
		return nil, err
	}
	json.Unmarshal([]byte(rawScopes), &r.scopes) //nolint:errcheck
	if t, err := time.Parse(time.RFC3339, authTimeStr); err == nil {
		r.authTime = t
	}
	if cc != "" {
		r.codeChallenge = &oidc.CodeChallenge{
			Challenge: cc,
			Method:    oidc.CodeChallengeMethod(ccMethod),
		}
	}
	r.done = isDone
	return &r, nil
}

func (s *Storage) SaveAuthCode(ctx context.Context, id, code string) error {
	r, err := s.authRequestByID(ctx, id)
	if err != nil {
		return err
	}
	scopes, _ := json.Marshal(r.scopes)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_authcodes(code, auth_req_id, client_id, user_id, scopes, redirect_uri, expires_at)
		 VALUES (?,?,?,?,?,?,?)`,
		code, id, r.clientID, r.userID, string(scopes), r.redirectURI,
		time.Now().Add(10*time.Minute).UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM oauth_auth_requests WHERE id = ?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE oauth_authcodes SET used_at = ? WHERE auth_req_id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// CompleteAuthRequest marque la requête comme complète avec le userID (appelé par le handler consent).
func (s *Storage) CompleteAuthRequest(ctx context.Context, id, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE oauth_auth_requests SET user_id = ?, auth_time = ? WHERE id = ?`,
		userID, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// ─── tokens ──────────────────────────────────────────────────────────────────

func (s *Storage) CreateAccessToken(ctx context.Context, req op.TokenRequest) (string, time.Time, error) {
	id := uuid.NewString()
	exp := time.Now().Add(time.Hour)
	scopes, _ := json.Marshal(req.GetScopes())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens(id, client_id, user_id, scopes, expires_at) VALUES (?,?,?,?,?)`,
		id, clientIDFromRequest(req), req.GetSubject(), string(scopes),
		exp.UTC().Format(time.RFC3339),
	)
	return id, exp, err
}

func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, req op.TokenRequest, currentRefreshToken string) (string, string, time.Time, error) {
	// Rotation : supprimer ancien refresh token
	if currentRefreshToken != "" {
		oldHash := hashSecret(currentRefreshToken)
		s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE refresh_token_hash = ?`, oldHash) //nolint:errcheck
	}

	id := uuid.NewString()
	exp := time.Now().Add(time.Hour)
	refreshToken, err := randomToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	scopes, _ := json.Marshal(req.GetScopes())
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens(id, refresh_token_hash, client_id, user_id, scopes, expires_at) VALUES (?,?,?,?,?,?)`,
		id, hashSecret(refreshToken), clientIDFromRequest(req), req.GetSubject(), string(scopes),
		exp.UTC().Format(time.RFC3339),
	)
	return id, refreshToken, exp, err
}

func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, refreshToken string) (op.RefreshTokenRequest, error) {
	hash := hashSecret(refreshToken)
	row := s.db.QueryRowContext(ctx,
		`SELECT id, client_id, user_id, scopes, expires_at FROM oauth_tokens WHERE refresh_token_hash = ? AND revoked_at IS NULL`,
		hash)
	var t tokenRow
	if err := row.Scan(&t.id, &t.clientID, &t.userID, &t.rawScopes, &t.expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, op.ErrInvalidRefreshToken
		}
		return nil, err
	}
	return &t, nil
}

type tokenRow struct {
	id        string
	clientID  string
	userID    string
	rawScopes string
	expiresAt string
	scopes    []string
}

func (t *tokenRow) GetAMR() []string                  { return nil }
func (t *tokenRow) GetAudience() []string              { return []string{t.clientID} }
func (t *tokenRow) GetAuthTime() time.Time             { return time.Time{} }
func (t *tokenRow) GetClientID() string                { return t.clientID }
func (t *tokenRow) GetScopes() []string                { t.parseScopes(); return t.scopes }
func (t *tokenRow) GetSubject() string                 { return t.userID }
func (t *tokenRow) SetCurrentScopes(s []string)        { t.scopes = s }
func (t *tokenRow) parseScopes() {
	if t.scopes == nil {
		json.Unmarshal([]byte(t.rawScopes), &t.scopes) //nolint:errcheck
	}
}

func (s *Storage) TerminateSession(ctx context.Context, userID, clientID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE oauth_tokens SET revoked_at = ? WHERE user_id = ? AND client_id = ? AND revoked_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339), userID, clientID)
	return err
}

func (s *Storage) RevokeToken(ctx context.Context, tokenOrTokenID, userID, clientID string) *oidc.Error {
	if userID != "" {
		// access token par tokenID
		s.db.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = ? WHERE id = ? AND user_id = ? AND client_id = ?`, //nolint:errcheck
			time.Now().UTC().Format(time.RFC3339), tokenOrTokenID, userID, clientID)
	} else {
		// refresh token par hash
		hash := hashSecret(tokenOrTokenID)
		s.db.ExecContext(ctx, `UPDATE oauth_tokens SET revoked_at = ? WHERE refresh_token_hash = ? AND client_id = ?`, //nolint:errcheck
			time.Now().UTC().Format(time.RFC3339), hash, clientID)
	}
	return nil
}

func (s *Storage) GetRefreshTokenInfo(_ context.Context, _ string, token string) (string, string, error) {
	// token = opaque refresh token. Si c'est un tokenID:userID → on décompose.
	// Pour notre implémentation, les refresh tokens sont opaques (hash en DB).
	return "", "", op.ErrInvalidRefreshToken
}

// ─── userinfo ─────────────────────────────────────────────────────────────────

func (s *Storage) SetUserinfoFromScopes(ctx context.Context, userinfo *oidc.UserInfo, userID, clientID string, scopes []string) error {
	return s.populateUserinfo(ctx, userinfo, userID, scopes)
}

func (s *Storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, _ string) error {
	row := s.db.QueryRowContext(ctx, `SELECT user_id, scopes FROM oauth_tokens WHERE id = ? AND revoked_at IS NULL`, tokenID)
	var uid, rawScopes string
	if err := row.Scan(&uid, &rawScopes); err != nil {
		return fmt.Errorf("%w: token %s", ErrNotFound, tokenID)
	}
	var scopes []string
	json.Unmarshal([]byte(rawScopes), &scopes) //nolint:errcheck
	return s.populateUserinfo(ctx, userinfo, uid, scopes)
}

func (s *Storage) SetIntrospectionFromToken(ctx context.Context, resp *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	row := s.db.QueryRowContext(ctx,
		`SELECT user_id, client_id, scopes, expires_at, revoked_at FROM oauth_tokens WHERE id = ?`, tokenID)
	var uid, cid, rawScopes, expiresAt string
	var revokedAt sql.NullString
	if err := row.Scan(&uid, &cid, &rawScopes, &expiresAt, &revokedAt); err != nil {
		resp.Active = false
		return nil
	}
	if revokedAt.Valid {
		resp.Active = false
		return nil
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().After(exp) {
		resp.Active = false
		return nil
	}
	resp.Active = true
	resp.ClientID = cid
	var scopes []string
	json.Unmarshal([]byte(rawScopes), &scopes) //nolint:errcheck
	resp.Scope = oidc.SpaceDelimitedArray(scopes)

	userinfo := new(oidc.UserInfo)
	if err := s.populateUserinfo(ctx, userinfo, uid, scopes); err == nil {
		resp.SetUserInfo(userinfo)
	}
	return nil
}

func (s *Storage) populateUserinfo(ctx context.Context, userinfo *oidc.UserInfo, userID string, scopes []string) error {
	row := s.db.QueryRowContext(ctx, `SELECT id, email, display_name FROM users WHERE id = ?`, userID)
	var id, email, displayName string
	if err := row.Scan(&id, &email, &displayName); err != nil {
		return fmt.Errorf("userinfo: user %s: %w", userID, err)
	}
	userinfo.Subject = id
	for _, sc := range scopes {
		switch sc {
		case oidc.ScopeEmail:
			userinfo.Email = email
			userinfo.EmailVerified = oidc.Bool(true)
		case oidc.ScopeProfile:
			userinfo.Name = displayName
		}
	}
	// Inclure les permissions RBAC effectives comme claims privés
	if s.rbacStore != nil {
		if err := s.injectRBACScopes(ctx, userinfo, userID, scopes); err != nil {
			// non-fatal
			_ = err
		}
	}
	return nil
}

func (s *Storage) injectRBACScopes(ctx context.Context, userinfo *oidc.UserInfo, userID string, scopes []string) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.name FROM user_effective_permissions uep
		 JOIN permissions p ON p.id = uep.permission_id
		 WHERE uep.user_id = ?`, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		rows.Scan(&p) //nolint:errcheck
		perms = append(perms, p)
	}
	if len(perms) > 0 {
		userinfo.AppendClaims("permissions", perms)
	}
	return rows.Err()
}

func (s *Storage) GetPrivateClaimsFromScopes(ctx context.Context, userID, clientID string, scopes []string) (map[string]any, error) {
	claims := map[string]any{}
	if s.rbacStore == nil {
		return claims, nil
	}
	// Intersect scopes with user effective permissions
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.name FROM user_effective_permissions uep
		 JOIN permissions p ON p.id = uep.permission_id
		 WHERE uep.user_id = ?`, userID)
	if err != nil {
		return claims, nil
	}
	defer rows.Close()
	var perms []string
	for rows.Next() {
		var p string
		rows.Scan(&p) //nolint:errcheck
		perms = append(perms, p)
	}
	if len(perms) > 0 {
		claims["permissions"] = perms
	}
	return claims, nil
}

func (s *Storage) GetKeyByIDAndClientID(_ context.Context, _, _ string) (*jose.JSONWebKey, error) {
	return nil, errors.New("oauth: JWT profile not supported")
}

func (s *Storage) ValidateJWTProfileScopes(ctx context.Context, userID string, scopes []string) ([]string, error) {
	return scopes, nil
}

// ─── admin helpers ────────────────────────────────────────────────────────────

// CreateClient insère un nouveau client OAuth en DB (helper pour tests et admin).
func (s *Storage) CreateClient(ctx context.Context, clientID, clientSecret string, redirectURIs, grantTypes, scopes []string) error {
	rr, _ := json.Marshal(redirectURIs)
	gg, _ := json.Marshal(grantTypes)
	ss, _ := json.Marshal(scopes)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_clients(client_id, client_secret_hash, redirect_uris, grant_types, scopes) VALUES (?,?,?,?,?)`,
		clientID, hashSecret(clientSecret), string(rr), string(gg), string(ss))
	return err
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	return RandomToken()
}

// RandomToken génère un token aléatoire de 32 octets encodé en hexadécimal.
func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func clientIDFromRequest(req op.TokenRequest) string {
	if ar, ok := req.(op.AuthRequest); ok {
		return ar.GetClientID()
	}
	if rt, ok := req.(op.RefreshTokenRequest); ok {
		return rt.GetClientID()
	}
	return ""
}
