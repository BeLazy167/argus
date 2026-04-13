package crypto

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

var validHexKey = hex.EncodeToString(make([]byte, 32))

func TestInit(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantErr   bool
		errSubstr string
	}{
		{"valid hex key", hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), false, ""},
		{"valid base64 key", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")), false, ""},
		{"wrong length 16 bytes hex", hex.EncodeToString(make([]byte, 16)), true, "32 bytes"},
		{"empty string", "", true, "not set"},
		{"invalid encoding", "not-hex-and-not-base64!!!", true, "hex or base64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Init(tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestEncryptDecrypt(t *testing.T) {
	if err := Init(validHexKey); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"basic round-trip", "hello world"},
		{"empty plaintext", ""},
		{"long plaintext", strings.Repeat("a", 1000)},
		{"unicode plaintext", "こんにちは世界 🌍 émojis"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt error: %v", err)
			}
			pt, err := Decrypt(ct)
			if err != nil {
				t.Fatalf("Decrypt error: %v", err)
			}
			if pt != tt.plaintext {
				t.Errorf("got %q, want %q", pt, tt.plaintext)
			}
		})
	}

	t.Run("non-deterministic ciphertext", func(t *testing.T) {
		ct1, err := Encrypt("same")
		if err != nil {
			t.Fatalf("Encrypt error: %v", err)
		}
		ct2, err := Encrypt("same")
		if err != nil {
			t.Fatalf("Encrypt error: %v", err)
		}
		if ct1 == ct2 {
			t.Error("two encryptions of same plaintext produced identical ciphertext")
		}
	})
}

func TestDecrypt(t *testing.T) {
	if err := Init(validHexKey); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	t.Run("invalid base64", func(t *testing.T) {
		_, err := Decrypt("not-valid-base64!!!")
		if err == nil {
			t.Fatal("expected error for invalid base64")
		}
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		ct, err := Encrypt("secret")
		if err != nil {
			t.Fatalf("Encrypt error: %v", err)
		}
		raw, _ := base64.StdEncoding.DecodeString(ct)
		// flip a byte in the ciphertext portion (after nonce)
		raw[len(raw)-1] ^= 0xFF
		tampered := base64.StdEncoding.EncodeToString(raw)
		_, err = Decrypt(tampered)
		if err == nil {
			t.Fatal("expected error for tampered ciphertext")
		}
	})

	t.Run("too short ciphertext", func(t *testing.T) {
		short := base64.StdEncoding.EncodeToString([]byte("tiny"))
		_, err := Decrypt(short)
		if err == nil {
			t.Fatal("expected error for short ciphertext")
		}
		if !strings.Contains(err.Error(), "too short") {
			t.Errorf("error %q should contain 'too short'", err.Error())
		}
	})
}
