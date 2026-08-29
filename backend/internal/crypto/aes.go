package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

var key []byte

// Init loads the 32-byte AES key from ENCRYPTION_KEY env var (hex-encoded).
func Init(envKey string) (err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(context.Background(), level, "encryption key initialization completed", "configured", err == nil, "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
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
func Encrypt(plaintext string) (encoded string, err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelDebug
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(context.Background(), level, "AES encryption completed", "plaintext_bytes", len(plaintext), "ciphertext_bytes", len(encoded), "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
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
func Decrypt(encoded string) (plaintext string, err error) {
	started := time.Now()
	defer func() {
		level := slog.LevelDebug
		if err != nil {
			level = slog.LevelError
		}
		slog.Log(context.Background(), level, "AES decryption completed", "ciphertext_bytes", len(encoded), "plaintext_bytes", len(plaintext), "duration_ms", time.Since(started).Milliseconds(), "error", err)
	}()
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
	decrypted, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
	if err != nil {
		return "", err
	}
	plaintext = string(decrypted)
	return plaintext, nil
}
