package util

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"time"
)

func exportSigningKey() []byte {
	return []byte(os.Getenv("WEBHOOK_SECRET"))
}

func computeExportSig(reviewID string, exp int64, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%s:%d", reviewID, exp)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SignExportURL generates an HMAC-signed export URL for public access.
// Links expire after ttl. Uses WEBHOOK_SECRET for signing.
func SignExportURL(reviewID, format string, ttl time.Duration) string {
	exp := time.Now().Add(ttl).Unix()
	sig := computeExportSig(reviewID, exp, exportSigningKey())
	return fmt.Sprintf("https://api.argus.reviews/api/v1/reviews/%s/export?format=%s&sig=%s&exp=%d",
		reviewID, format, sig, exp)
}

// VerifyExportSig checks an HMAC signature + expiry for export URLs.
func VerifyExportSig(reviewID, sig, expStr string) bool {
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
	expected := computeExportSig(reviewID, exp, exportSigningKey())
	return hmac.Equal([]byte(sig), []byte(expected))
}
