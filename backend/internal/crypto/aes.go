package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

var key []byte

// Init loads the 32-byte AES key from ENCRYPTION_KEY env var (hex-encoded).
func Init(envKey string) error {
	if envKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY is not set")
	}
	k, err := hex.DecodeString(envKey)
	if err != nil {
		// try base64
		k, err = base64.StdEncoding.DecodeString(envKey)
		if err != nil {
			return fmt.Errorf("ENCRYPTION_KEY must be 32-byte hex or base64")
		}
	}
	if len(k) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes, got %d", len(k))
	}
	key = k
	return nil
}

// InitFromEnv is a convenience that reads ENCRYPTION_KEY from os env.
func InitFromEnv() error {
	return Init(os.Getenv("ENCRYPTION_KEY"))
}

// Encrypt encrypts plaintext with AES-256-GCM, returns base64-encoded ciphertext.
func Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded AES-256-GCM ciphertext.
func Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
