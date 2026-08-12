package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/BeLazy167/argus/backend/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func seedRuleHandlerBoundary(t *testing.T, ctx context.Context, pool *pgxpool.Pool, suffix string) (int64, int64, string) {
	t.Helper()
	var outboxExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('memory_mirror_outbox') IS NOT NULL`).Scan(&outboxExists); err != nil {
		t.Fatal(err)
	}
	if !outboxExists {
		t.Skip("migration 075 is supplied by the integration branch")
	}

	var installationID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO installations (installation_id, org_login)
		VALUES ((random()*1000000000)::bigint, $1) RETURNING id`,
		"rule-handler-boundary-"+suffix+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)).Scan(&installationID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM memory_mirror_outbox WHERE installation_id=$1`, installationID)
		_, _ = pool.Exec(bg, `DELETE FROM memories WHERE installation_id=$1`, installationID)
		_, _ = pool.Exec(bg, `DELETE FROM rules WHERE installation_id=$1`, installationID)
		_, _ = pool.Exec(bg, `DELETE FROM installations WHERE id=$1`, installationID)
	})

	st := store.NewWithDB(pool)
	rule, err := st.CreateRule(ctx, installationID, "safety", "handler success means unsearchable", 5, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM activity_log WHERE resource=$1`, fmt.Sprintf("rule:%d", rule.ID))
	})
	customID := fmt.Sprintf("rule--%d", rule.ID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO memories (installation_id, container_tag, custom_id, type, content)
		VALUES ($1, '_shared', $2, 'rule', 'handler success means unsearchable')`, installationID, customID); err != nil {
		t.Fatal(err)
	}
	return installationID, rule.ID, customID
}

func TestRuleMutationAPIReportsSuccessOnlyAfterMemoryIsUnsearchable(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		body       string
		invoke     func(*Server, http.ResponseWriter, *http.Request)
		assertRule func(*testing.T, context.Context, *pgxpool.Pool, int64)
	}{
		{
			name:   "disable",
			method: http.MethodPut,
			body:   `{"enabled":false}`,
			invoke: (*Server).updateRule,
			assertRule: func(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ruleID int64) {
				var enabled bool
				if err := pool.QueryRow(ctx, `SELECT enabled FROM rules WHERE id=$1`, ruleID).Scan(&enabled); err != nil {
					t.Fatal(err)
				}
				if enabled {
					t.Fatal("API returned success but the rule remained enabled")
				}
			},
		},
		{
			name:   "delete",
			method: http.MethodDelete,
			invoke: (*Server).deleteRule,
			assertRule: func(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ruleID int64) {
				var exists bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rules WHERE id=$1)`, ruleID).Scan(&exists); err != nil {
					t.Fatal(err)
				}
				if exists {
					t.Fatal("API returned success but the rule still existed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, ctx := architectureTestPool(t)
			installationID, ruleID, customID := seedRuleHandlerBoundary(t, ctx, pool, tt.name)
			s := &Server{
				store:  store.NewWithDB(pool),
				logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			routeCtx := chi.NewRouteContext()
			routeCtx.URLParams.Add("ruleID", strconv.FormatInt(ruleID, 10))
			requestCtx := context.WithValue(ctx, chi.RouteCtxKey, routeCtx)
			requestCtx = context.WithValue(requestCtx, installationIDsKey, []int64{installationID})
			req := httptest.NewRequest(tt.method, "/api/v1/rules/"+strconv.FormatInt(ruleID, 10), bytes.NewBufferString(tt.body)).WithContext(requestCtx)
			recorder := httptest.NewRecorder()

			tt.invoke(s, recorder, req)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var live bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM live_memories WHERE installation_id=$1 AND custom_id=$2)`,
				installationID, customID).Scan(&live); err != nil {
				t.Fatal(err)
			}
			if live {
				t.Fatal("API returned success while the rule memory was still searchable")
			}
			tt.assertRule(t, ctx, pool, ruleID)
		})
	}
}
