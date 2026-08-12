package store

import (
	"reflect"
	"testing"
)

func TestSanitizedPGXDetailsKeepsAllArguments(t *testing.T) {
	args := []any{"encrypted-secret", int64(42), map[string]any{"token": "raw-token"}}
	input := map[string]any{
		"sql":  "INSERT INTO provider_keys (api_key_enc) VALUES ($1)",
		"args": args,
	}
	got := sanitizedPGXDetails(input)
	if !reflect.DeepEqual(got["args"], args) {
		t.Fatalf("args = %#v, want %#v", got["args"], args)
	}
}
