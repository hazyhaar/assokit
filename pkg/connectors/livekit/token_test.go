package livekit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRoomToken_StructureAndSignature(t *testing.T) {
	const key, secret = "APIabc", "s3cr3t-livekit"
	now := time.Unix(1_700_000_000, 0)
	tok, err := roomTokenAt(key, secret, "salle-1", "user-42", time.Hour, now)
	if err != nil {
		t.Fatalf("RoomToken: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT mal formé: %d segments", len(parts))
	}

	// Signature HMAC-SHA256 valide sur header.claims.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Fatalf("signature invalide")
	}

	// Claims décodables + grants LiveKit attendus.
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var c claims
	if err := json.Unmarshal(cb, &c); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if c.Iss != key || c.Sub != "user-42" {
		t.Fatalf("iss/sub: %s/%s", c.Iss, c.Sub)
	}
	if c.Video.Room != "salle-1" || !c.Video.RoomJoin {
		t.Fatalf("video grant incorrect: %+v", c.Video)
	}
	if c.Exp != now.Add(time.Hour).Unix() {
		t.Fatalf("exp=%d", c.Exp)
	}
}

func TestRoomToken_Validation(t *testing.T) {
	if _, err := RoomToken("", "s", "r", "i", time.Hour); err == nil {
		t.Error("api_key vide doit échouer")
	}
	if _, err := RoomToken("k", "s", "", "i", time.Hour); err == nil {
		t.Error("room vide doit échouer")
	}
}
