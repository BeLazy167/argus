package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type providerKeyDBTX struct{ rows pgx.Rows }

func (d providerKeyDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}
func (d providerKeyDBTX) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return d.rows, nil
}
func (d providerKeyDBTX) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	panic("unexpected QueryRow")
}

type providerKeyRows struct{ next bool }

func (r *providerKeyRows) Close()                                       {}
func (r *providerKeyRows) Err() error                                   { return nil }
func (r *providerKeyRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *providerKeyRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *providerKeyRows) Next() bool {
	if r.next {
		return false
	}
	r.next = true
	return true
}
func (r *providerKeyRows) Scan(dest ...any) error {
	if len(dest) != 10 {
		return fmt.Errorf("scan destinations = %d, want 10", len(dest))
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	repoID := int64(7)
	baseURL, model, keyHint := "https://llm.example", "model-v1", "encrypted-hint"
	values := []any{int64(11), int64(22), &repoID, "openai", "encrypted-key", &baseURL, &model, &keyHint, now, now}
	for i := range dest {
		switch out := dest[i].(type) {
		case *int64:
			*out = values[i].(int64)
		case **int64:
			*out = values[i].(*int64)
		case *string:
			*out = values[i].(string)
		case **string:
			*out = values[i].(*string)
		case *time.Time:
			*out = values[i].(time.Time)
		default:
			return fmt.Errorf("destination %d has type %T", i, dest[i])
		}
	}
	return nil
}
func (r *providerKeyRows) Values() ([]any, error) { return nil, nil }
func (r *providerKeyRows) RawValues() [][]byte    { return nil }
func (r *providerKeyRows) Conn() *pgx.Conn        { return nil }

func TestListProviderKeysUsesGeneratedColumnMapping(t *testing.T) {
	st := NewWithDB(providerKeyDBTX{rows: &providerKeyRows{}})

	keys, err := st.ListProviderKeys(context.Background(), 22)
	if err != nil {
		t.Fatalf("ListProviderKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("len(keys) = %d, want 1", len(keys))
	}
	got := keys[0]
	if got.KeyHint != "encrypted-hint" {
		t.Errorf("KeyHint = %q", got.KeyHint)
	}
	if got.BaseURL == nil || *got.BaseURL != "https://llm.example" {
		t.Errorf("BaseURL = %v", got.BaseURL)
	}
	if got.Model == nil || *got.Model != "model-v1" {
		t.Errorf("Model = %v", got.Model)
	}
	if _, err := json.Marshal(keys); err != nil {
		t.Fatalf("marshal keys: %v", err)
	}
}
