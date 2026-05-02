// CLAUDE:SUMMARY AES-256-GCM encrypt/decrypt + master key validation (M-ASSOKIT-SPRINT2-S2).
// CLAUDE:WARN Master key change post-prod = perte définitive des credentials existants (impossible re-chiffrer sans plaintext).
package assets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	// MasterKeyHexLen : 32 bytes = 64 chars hex.
	MasterKeyHexLen = 64
	// MasterKeyByteLen : taille AES-256 en bytes.
	MasterKeyByteLen = 32
)

// Errors sentinelles.
var (
	ErrMasterKeyMissing = errors.New("connectors/assets: NPS_MASTER_KEY env absent")
	ErrMasterKeyLength  = errors.New("connectors/assets: NPS_MASTER_KEY doit faire 64 chars hex (32 bytes)")
	ErrMasterKeyInvalid = errors.New("connectors/assets: NPS_MASTER_KEY n'est pas un hex valide")
)

// DecodeMasterKey décode un master key hex 64-char vers []byte 32-byte.
// Erreurs typées pour différencier missing/length/invalid.
func DecodeMasterKey(hexKey string) ([]byte, error) {
	if hexKey == "" {
		return nil, ErrMasterKeyMissing
	}
	if len(hexKey) != MasterKeyHexLen {
		return nil, fmt.Errorf("%w (got %d chars)", ErrMasterKeyLength, len(hexKey))
	}
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMasterKeyInvalid, err)
	}
	return key, nil
}

// Encrypt chiffre plaintext avec AES-256-GCM. Retourne nonce|ciphertext|tag (concaténés).
// Le nonce (12 bytes) est généré aléatoirement à chaque appel.
func Encrypt(masterKey []byte, plaintext []byte) ([]byte, error) {
	if len(masterKey) != MasterKeyByteLen {
		return nil, fmt.Errorf("master key length %d != %d", len(masterKey), MasterKeyByteLen)
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("rand nonce: %w", err)
	}
	// Seal append au nonce → format final = nonce | ciphertext | tag.
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt déchiffre nonce|ciphertext|tag avec AES-256-GCM.
// Erreur si master key incorrecte (auth tag mismatch).
func Decrypt(masterKey []byte, encrypted []byte) ([]byte, error) {
	if len(masterKey) != MasterKeyByteLen {
		return nil, fmt.Errorf("master key length %d != %d", len(masterKey), MasterKeyByteLen)
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(encrypted) < nonceSize {
		return nil, errors.New("ciphertext trop court (manque nonce)")
	}
	nonce, ct := encrypted[:nonceSize], encrypted[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return plaintext, nil
}

// ZeroBytes écrit 0 sur tout le slice. Utilisé pour wipe plaintext post-callback.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
