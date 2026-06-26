// Package livekit fournit un connecteur optionnel vers un serveur LiveKit
// (visioconférence). assokit génère les jetons d'accès aux salles et sert l'UI ;
// le serveur LiveKit est un déploiement voisin.
//
// Import-clean (publiable) : le jeton est un JWT HS256 forgé en stdlib pure
// (crypto/hmac + encoding/json + base64url), sans SDK externe.
package livekit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// videoGrant décrit les droits LiveKit accordés dans le jeton (claim "video").
type videoGrant struct {
	Room         string `json:"room"`
	RoomJoin     bool   `json:"roomJoin,omitempty"`
	CanPublish   bool   `json:"canPublish,omitempty"`
	CanSubscribe bool   `json:"canSubscribe,omitempty"`
	RoomAdmin    bool   `json:"roomAdmin,omitempty"`
	Hidden       bool   `json:"hidden,omitempty"`
}

// claims est le corps JWT attendu par LiveKit.
type claims struct {
	Iss   string     `json:"iss"`  // API key
	Sub   string     `json:"sub"`  // identité du participant
	Name  string     `json:"name"` // nom affiché
	Nbf   int64      `json:"nbf"`
	Exp   int64      `json:"exp"`
	Video videoGrant `json:"video"`
}

// RoomToken forge un jeton d'accès LiveKit (JWT HS256) pour rejoindre `room`
// sous l'identité `identity`, valable `ttl`. apiKey/apiSecret proviennent du
// Vault. Erreur si un paramètre obligatoire manque.
func RoomToken(apiKey, apiSecret, room, identity string, ttl time.Duration) (string, error) {
	return roomTokenAt(apiKey, apiSecret, room, identity, ttl, time.Now())
}

// roomTokenAt est la variante testable (horodatage injecté).
func roomTokenAt(apiKey, apiSecret, room, identity string, ttl time.Duration, now time.Time) (string, error) {
	if apiKey == "" || apiSecret == "" {
		return "", errors.New("livekit: api_key/api_secret requis")
	}
	if room == "" || identity == "" {
		return "", errors.New("livekit: room et identity requis")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	c := claims{
		Iss:  apiKey,
		Sub:  identity,
		Name: identity,
		Nbf:  now.Add(-30 * time.Second).Unix(), // marge d'horloge
		Exp:  now.Add(ttl).Unix(),
		Video: videoGrant{
			Room:         room,
			RoomJoin:     true,
			CanPublish:   true,
			CanSubscribe: true,
		},
	}

	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal header: %w", err)
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal claims: %w", err)
	}

	signingInput := b64(hb) + "." + b64(cb)
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingInput))
	sig := b64(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// RoomAdminToken forge un jeton d'administration LiveKit (JWT HS256) portant
// roomAdmin:true pour la salle `room`. Ce jeton est utilisé pour appeler le
// RoomService Twirp (mute, remove, delete). Il ne porte PAS roomJoin ni
// CanPublish/CanSubscribe — il est distinct du jeton participant.
func RoomAdminToken(apiKey, apiSecret, room string, ttl time.Duration) (string, error) {
	return roomAdminTokenAt(apiKey, apiSecret, room, ttl, time.Now())
}

// roomAdminTokenAt est la variante testable (horodatage injecté).
func roomAdminTokenAt(apiKey, apiSecret, room string, ttl time.Duration, now time.Time) (string, error) {
	if apiKey == "" || apiSecret == "" {
		return "", errors.New("livekit: api_key/api_secret requis")
	}
	if room == "" {
		return "", errors.New("livekit: room requis")
	}
	if ttl <= 0 {
		ttl = time.Minute
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	c := claims{
		Iss:  apiKey,
		Sub:  apiKey, // identité = la clé API elle-même pour un jeton d'admin
		Name: apiKey,
		Nbf:  now.Add(-30 * time.Second).Unix(),
		Exp:  now.Add(ttl).Unix(),
		Video: videoGrant{
			Room:      room,
			RoomAdmin: true,
		},
	}

	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal header: %w", err)
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal claims: %w", err)
	}

	signingInput := b64(hb) + "." + b64(cb)
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingInput))
	sig := b64(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// RoomHiddenToken forge un jeton d'accès LiveKit pour un participant caché
// (hidden:true, CanPublish:false, CanSubscribe:true). Ce jeton est réservé aux
// bots pôle-only (transcriber, recorder) qui ne doivent pas apparaître dans la
// grille des participants. Pôle-only : ne jamais exporter dans le kit open-source.
func RoomHiddenToken(apiKey, apiSecret, room, identity string, ttl time.Duration) (string, error) {
	return roomHiddenTokenAt(apiKey, apiSecret, room, identity, ttl, time.Now())
}

func roomHiddenTokenAt(apiKey, apiSecret, room, identity string, ttl time.Duration, now time.Time) (string, error) {
	if apiKey == "" || apiSecret == "" {
		return "", errors.New("livekit: api_key/api_secret requis")
	}
	if room == "" || identity == "" {
		return "", errors.New("livekit: room et identity requis")
	}
	if ttl <= 0 {
		ttl = time.Hour
	}

	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	c := claims{
		Iss:  apiKey,
		Sub:  identity,
		Name: identity,
		Nbf:  now.Add(-30 * time.Second).Unix(),
		Exp:  now.Add(ttl).Unix(),
		Video: videoGrant{
			Room:         room,
			RoomJoin:     true,
			CanPublish:   false,
			CanSubscribe: true,
			Hidden:       true,
		},
	}

	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal header: %w", err)
	}
	cb, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("livekit: marshal claims: %w", err)
	}

	signingInput := b64(hb) + "." + b64(cb)
	mac := hmac.New(sha256.New, []byte(apiSecret))
	mac.Write([]byte(signingInput))
	sig := b64(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// b64 encode en base64url sans padding (format JWT).
func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
