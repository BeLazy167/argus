package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// missingKeyWarnOnce gates the WEBHOOK_SECRET warning to a single log line.
var missingKeyWarnOnce sync.Once

// exportSigningKey returns the HMAC key for export URLs, or nil when
// WEBHOOK_SECRET is unset. Callers must treat nil as "signing disabled" —
// signing with an empty key would make every signature forgeable.
func exportSigningKey() []byte {
	key := os.Getenv("WEBHOOK_SECRET")
	if key == "" {
		missingKeyWarnOnce.Do(func() {
			slog.Warn("WEBHOOK_SECRET is not set; signed export URLs are disabled")
		})
		return nil
	}
	return []byte(key)
}

func computeExportSig(reviewID string, exp int64, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%s:%d", reviewID, exp)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignExportURL generates an HMAC-signed export URL for public access.
// Links expire after ttl. Uses WEBHOOK_SECRET for signing; returns "" when
// the secret is unset (fail closed — never mint a forgeable URL).
func SignExportURL(apiBaseURL, reviewID, format string, ttl time.Duration) string {
	key := exportSigningKey()
	if key == nil {
		return ""
	}
	exp := time.Now().Add(ttl).Unix()
	sig := computeExportSig(reviewID, exp, key)
	return fmt.Sprintf("%s/api/v1/reviews/%s/export?format=%s&sig=%s&exp=%d",
		apiBaseURL, reviewID, format, sig, exp)
}

// VerifyExportSig checks an HMAC signature + expiry for export URLs.
// Always false when WEBHOOK_SECRET is unset (fail closed).
func VerifyExportSig(reviewID, sig, expStr string) bool {
	key := exportSigningKey()
	if key == nil {
		return false
	}
	if sig == "" || expStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > exp {
		return false
	}
	expected := computeExportSig(reviewID, exp, key)
	return hmac.Equal([]byte(sig), []byte(expected))
}
