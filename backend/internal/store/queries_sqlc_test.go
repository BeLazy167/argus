package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ownerPrefixDBTX struct{ arg any }

func (d *ownerPrefixDBTX) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec")
}
func (d *ownerPrefixDBTX) Query(_ context.Context, _ string, args ...interface{}) (pgx.Rows, error) {
	d.arg = args[0]
	return &providerKeyRows{next: true}, nil
}
func (d *ownerPrefixDBTX) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	panic("unexpected QueryRow")
}

func TestListReposByOwnerEscapesLikeWildcardsBeforeSQLC(t *testing.T) {
	dbtx := &ownerPrefixDBTX{}
	st := NewWithDB(dbtx)

	rows, err := st.ListReposByOwner(context.Background(), `acme_%`)
	if err != nil {
		t.Fatalf("ListReposByOwner: %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("rows = %#v, want non-nil empty slice", rows)
	}
	if got, want := dbtx.arg, `acme\_\%/%`; got != want {
		t.Fatalf("query prefix = %q, want %q", got, want)
	}
}
