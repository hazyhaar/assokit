// CLAUDE:SUMMARY Amorçage OAuth des comptes de publication sociaux (X, TikTok,
// YouTube, LinkedIn). Handler générique paramétré par réseau : route d'initiation
// (génère state anti-CSRF + verifier PKCE, redirige vers AuthorizeURL) et route de
// rappel (valide le state, échange le code via le connecteur qui persiste le jeton au
// coffre). État OAuth porté par un COOKIE COURT signé HMAC, jamais par la DB.
// CLAUDE:WARN Routes gardées par requireAdmin : amorcer un compte de publication est
// un geste d'administration. Aucun secret journalisé (state, verifier, code, jeton).
// Meta est volontairement EXCLU (amorçage = procédure opérateur, cf. addLinkedIn… docs).
package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	intoauth "github.com/hazyhaar/assokit/internal/oauth"
)

const (
	// socialStateCookiePrefix préfixe le nom du cookie d'état (un par réseau, pour
	// que deux amorçages concurrents sur des réseaux distincts ne s'écrasent pas).
	socialStateCookiePrefix = "social_oauth_"
	// socialStateTTL borne la durée de vie du cookie d'état : l'aller-retour vers le
	// fournisseur est court ; au-delà, l'état est rejeté.
	socialStateTTL = 10 * time.Minute
)

// SocialConnector est l'adaptateur de bordure d'un connecteur de publication, exposé
// au handler générique sous forme de fonctions. Cette indirection découple le paquet
// handlers des paquets de connecteurs (pas d'import) : la bordure (cmd) construit les
// adaptateurs à partir des types concrets. Pour les réseaux sans PKCE (YouTube,
// LinkedIn), UsesPKCE est faux et le verifier passé à Authorize/Exchange est ignoré.
type SocialConnector struct {
	// Network est l'identifiant stable du réseau (clé d'URL : x, tiktok, youtube, linkedin).
	Network string
	// UsesPKCE indique si le réseau exige un code_verifier PKCE (X, TikTok).
	UsesPKCE bool
	// Authorize construit l'URL d'autorisation (le verifier est ignoré si !UsesPKCE).
	Authorize func(ctx context.Context, state, verifier string) (string, error)
	// Exchange échange le code contre les jetons et les PERSISTE au coffre (le verifier
	// est ignoré si !UsesPKCE). Retourne l'access_token (non journalisé).
	Exchange func(ctx context.Context, code, verifier string) (string, error)
}

// MountSocialConnect câble les routes d'amorçage OAuth des comptes de publication sur
// le routeur fourni, gardées par requireAdmin. cookieSecret signe le cookie d'état ;
// secure pilote l'attribut Secure du cookie (true derrière HTTPS).
func MountSocialConnect(r chi.Router, cookieSecret []byte, secure bool, logger *slog.Logger, conns ...SocialConnector) {
	if logger == nil {
		logger = slog.Default()
	}
	byNet := make(map[string]SocialConnector, len(conns))
	for _, c := range conns {
		byNet[c.Network] = c
	}
	r.With(requireAdmin).Get("/connect/{network}/start", handleSocialConnectStart(byNet, cookieSecret, secure, logger))
	r.With(requireAdmin).Get("/connect/{network}/callback", handleSocialConnectCallback(byNet, cookieSecret, secure, logger))
}

// handleSocialConnectStart génère un state anti-CSRF (et un verifier PKCE si le réseau
// l'exige), les dépose dans un cookie court signé, puis redirige vers l'URL
// d'autorisation du fournisseur.
func handleSocialConnectStart(byNet map[string]SocialConnector, secret []byte, secure bool, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		network := chi.URLParam(r, "network")
		conn, ok := byNet[network]
		if !ok {
			http.Error(w, "réseau social inconnu ou non configuré", http.StatusNotFound)
			return
		}
		state, err := intoauth.RandomToken()
		if err != nil {
			logger.Error("social_connect_start", "network", network, "stage", "state", "err", err.Error())
			http.Error(w, "erreur interne", http.StatusInternalServerError)
			return
		}
		var verifier string
		if conn.UsesPKCE {
			verifier, err = intoauth.RandomToken()
			if err != nil {
				logger.Error("social_connect_start", "network", network, "stage", "verifier", "err", err.Error())
				http.Error(w, "erreur interne", http.StatusInternalServerError)
				return
			}
		}
		authURL, err := conn.Authorize(r.Context(), state, verifier)
		if err != nil {
			logger.Error("social_connect_start", "network", network, "stage", "authorize_url", "err", err.Error())
			http.Error(w, "construction de l'URL d'autorisation impossible", http.StatusBadGateway)
			return
		}
		setStateCookie(w, network, state, verifier, secret, secure)
		http.Redirect(w, r, authURL, http.StatusFound)
	}
}

// handleSocialConnectCallback valide le state (cookie signé vs query), récupère le
// verifier PKCE le cas échéant, et échange le code — le connecteur persiste le jeton au
// coffre. Le cookie d'état est effacé après usage, succès ou échec.
func handleSocialConnectCallback(byNet map[string]SocialConnector, secret []byte, secure bool, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		network := chi.URLParam(r, "network")
		conn, ok := byNet[network]
		if !ok {
			http.Error(w, "réseau social inconnu ou non configuré", http.StatusNotFound)
			return
		}

		cookie, err := r.Cookie(socialStateCookiePrefix + network)
		if err != nil {
			http.Error(w, "état d'autorisation absent (cookie manquant ou expiré)", http.StatusBadRequest)
			return
		}
		cookieState, verifier, valid := decodeStateCookie(secret, cookie.Value)
		// Le cookie est à usage unique : on l'efface quoi qu'il arrive.
		clearStateCookie(w, network, secure)
		if !valid {
			http.Error(w, "état d'autorisation invalide ou expiré", http.StatusBadRequest)
			return
		}

		queryState := r.URL.Query().Get("state")
		if queryState == "" || subtle.ConstantTimeCompare([]byte(queryState), []byte(cookieState)) != 1 {
			logger.Warn("social_connect_callback", "network", network, "stage", "state_mismatch")
			http.Error(w, "état d'autorisation non concordant", http.StatusBadRequest)
			return
		}
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			logger.Warn("social_connect_callback", "network", network, "stage", "provider_error", "error", errParam)
			http.Error(w, "le fournisseur a refusé l'autorisation", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "code d'autorisation absent", http.StatusBadRequest)
			return
		}

		if _, err := conn.Exchange(r.Context(), code, verifier); err != nil {
			logger.Error("social_connect_callback", "network", network, "stage", "exchange", "err", err.Error())
			http.Error(w, "échange du code d'autorisation échoué", http.StatusBadGateway)
			return
		}
		logger.Info("social_connect_callback", "network", network, "stage", "token_persisted")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Compte " + network + " connecté : le jeton de publication a été enregistré."))
	}
}

// setStateCookie pose le cookie d'état signé. Le contenu (exp|state|verifier) est signé
// HMAC-SHA256 avec le secret de cookie ; le verifier est vide pour les réseaux sans PKCE.
// Path restreint au sous-arbre du réseau ; SameSite=Lax pour survivre à la redirection
// de retour (navigation GET de premier niveau depuis le fournisseur).
func setStateCookie(w http.ResponseWriter, network, state, verifier string, secret []byte, secure bool) {
	exp := time.Now().Add(socialStateTTL).Unix()
	raw := strconv.FormatInt(exp, 10) + "|" + state + "|" + verifier
	value := base64.RawURLEncoding.EncodeToString([]byte(raw)) + "." + signState(secret, raw)
	http.SetCookie(w, &http.Cookie{
		Name:     socialStateCookiePrefix + network,
		Value:    value,
		Path:     "/connect/" + network,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(socialStateTTL / time.Second),
	})
}

// clearStateCookie efface le cookie d'état d'un réseau (usage unique).
func clearStateCookie(w http.ResponseWriter, network string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     socialStateCookiePrefix + network,
		Value:    "",
		Path:     "/connect/" + network,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

// decodeStateCookie vérifie la signature et la fraîcheur du cookie d'état et en extrait
// le state et le verifier. Retourne valid=false sur signature invalide, format altéré
// ou expiration dépassée.
func decodeStateCookie(secret []byte, value string) (state, verifier string, valid bool) {
	dot := strings.LastIndex(value, ".")
	if dot < 0 {
		return "", "", false
	}
	rawBytes, err := base64.RawURLEncoding.DecodeString(value[:dot])
	if err != nil {
		return "", "", false
	}
	raw := string(rawBytes)
	if !hmac.Equal([]byte(signState(secret, raw)), []byte(value[dot+1:])) {
		return "", "", false
	}
	parts := strings.SplitN(raw, "|", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// signState calcule la signature HMAC-SHA256 hexadécimale du contenu du cookie d'état.
func signState(secret []byte, payload string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
