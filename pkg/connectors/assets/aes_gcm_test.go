// CLAUDE:SUMMARY Tests gardiens AES-GCM + DecodeMasterKey (M-ASSOKIT-SPRINT2-S2).
package assets

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

const validHexKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// TestAESGCM_RoundtripProducesIdenticalPlaintext : encrypt+decrypt = original.
func TestAESGCM_RoundtripProducesIdenticalPlaintext(t *testing.T) {
	key, err := DecodeMasterKey(validHexKey)
	if err != nil {
		t.Fatalf("DecodeMasterKey: %v", err)
	}
	for _, plaintext := range []string{"", "a", "secret-12345", strings.Repeat("X", 1000)} {
		enc, err := Encrypt(key, []byte(plaintext))
		if err != nil {
			t.Fatalf("Encrypt %q: %v", plaintext, err)
		}
		dec, err := Decrypt(key, enc)
		if err != nil {
			t.Fatalf("Decrypt %q: %v", plaintext, err)
		}
		if !bytes.Equal(dec, []byte(plaintext)) {
			t.Errorf("roundtrip mismatch : got %q, want %q", dec, plaintext)
		}
	}
}

// TestAESGCM_DifferentNoncePerCall : Encrypt(même value) 2x → ciphertexts différents.
func TestAESGCM_DifferentNoncePerCall(t *testing.T) {
	key, _ := DecodeMasterKey(validHexKey)
	enc1, _ := Encrypt(key, []byte("hello"))
	enc2, _ := Encrypt(key, []byte("hello"))
	if bytes.Equal(enc1, enc2) {
		t.Error("Encrypt deux fois produit le même output (nonce fixe ?)")
	}
}

// TestAESGCM_WrongKeyFailsAuth : key altéré → Decrypt erreur (auth tag mismatch).
func TestAESGCM_WrongKeyFailsAuth(t *testing.T) {
	key1, _ := DecodeMasterKey(validHexKey)
	key2, _ := DecodeMasterKey(strings.Repeat("ff", 32))
	enc, _ := Encrypt(key1, []byte("secret"))
	_, err := Decrypt(key2, enc)
	if err == nil {
		t.Error("Decrypt avec mauvaise clé devrait échouer (auth tag mismatch)")
	}
}

// TestDecodeMasterKey_MissingErrTyped : env vide → ErrMasterKeyMissing.
func TestDecodeMasterKey_MissingErrTyped(t *testing.T) {
	_, err := DecodeMasterKey("")
	if !errors.Is(err, ErrMasterKeyMissing) {
		t.Errorf("DecodeMasterKey(\"\") err = %v, attendu ErrMasterKeyMissing", err)
	}
}

// TestDecodeMasterKey_InvalidLengthErrTyped : len != 64 → ErrMasterKeyLength.
func TestDecodeMasterKey_InvalidLengthErrTyped(t *testing.T) {
	_, err := DecodeMasterKey("0011")
	if !errors.Is(err, ErrMasterKeyLength) {
		t.Errorf("err short = %v, attendu ErrMasterKeyLength", err)
	}
}

// TestDecodeMasterKey_InvalidHexErrTyped : caractère non-hex → ErrMasterKeyInvalid.
func TestDecodeMasterKey_InvalidHexErrTyped(t *testing.T) {
	_, err := DecodeMasterKey(strings.Repeat("ZZ", 32))
	if !errors.Is(err, ErrMasterKeyInvalid) {
		t.Errorf("err invalid hex = %v, attendu ErrMasterKeyInvalid", err)
	}
}

// TestZeroBytes_ZerosSlice : ZeroBytes met à 0 tout le slice.
func TestZeroBytes_ZerosSlice(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5}
	ZeroBytes(b)
	for i, x := range b {
		if x != 0 {
			t.Errorf("b[%d] = %d, attendu 0", i, x)
		}
	}
}
