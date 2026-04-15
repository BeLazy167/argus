package util

import (
	"os"
	"testing"
	"time"
)

func TestSignAndVerifyExportURL(t *testing.T) {
	os.Setenv("WEBHOOK_SECRET", "test-secret-key-1234")
	defer os.Unsetenv("WEBHOOK_SECRET")

	reviewID := "abc-123-def"
	url := SignExportURL(reviewID, "md", 1*time.Hour)

	if url == "" {
		t.Fatal("SignExportURL returned empty string")
	}

	// Extract sig and exp from URL
	// URL format: ...?format=md&sig=XXX&exp=NNN
	parts := splitQuery(url)

	t.Run("valid signature verifies", func(t *testing.T) {
		if !VerifyExportSig(reviewID, parts["sig"], parts["exp"]) {
			t.Error("expected valid signature to verify")
		}
	})

	t.Run("wrong reviewID fails", func(t *testing.T) {
		if VerifyExportSig("wrong-id", parts["sig"], parts["exp"]) {
			t.Error("expected wrong reviewID to fail verification")
		}
	})

	t.Run("tampered signature fails", func(t *testing.T) {
		if VerifyExportSig(reviewID, parts["sig"]+"x", parts["exp"]) {
			t.Error("expected tampered sig to fail verification")
		}
	})

	t.Run("empty sig fails", func(t *testing.T) {
		if VerifyExportSig(reviewID, "", parts["exp"]) {
			t.Error("expected empty sig to fail")
		}
	})

	t.Run("empty exp fails", func(t *testing.T) {
		if VerifyExportSig(reviewID, parts["sig"], "") {
			t.Error("expected empty exp to fail")
		}
	})

	t.Run("invalid exp fails", func(t *testing.T) {
		if VerifyExportSig(reviewID, parts["sig"], "notanumber") {
			t.Error("expected invalid exp to fail")
		}
	})

	t.Run("expired signature fails", func(t *testing.T) {
		expiredURL := SignExportURL(reviewID, "md", -1*time.Second)
		ep := splitQuery(expiredURL)
		if VerifyExportSig(reviewID, ep["sig"], ep["exp"]) {
			t.Error("expected expired signature to fail")
		}
	})
}

func splitQuery(rawURL string) map[string]string {
	result := make(map[string]string)
	qIdx := 0
	for i, c := range rawURL {
		if c == '?' {
			qIdx = i + 1
			break
		}
	}
	if qIdx == 0 {
		return result
	}
	query := rawURL[qIdx:]
	for _, pair := range splitOn(query, '&') {
		kv := splitOn(pair, '=')
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
		}
	}
	return result
}

func splitOn(s string, sep byte) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
