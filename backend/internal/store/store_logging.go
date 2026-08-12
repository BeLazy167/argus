package store

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"time"
)

// beginStoreOperation records the semantic boundary around a Store method.
// Query-level details, including SQL arguments, remain the responsibility of
// the pgx tracer configured in postgres.go.
func beginStoreOperation(ctx context.Context, operation string, attrs ...any) func(error, ...any) {
	started := time.Now()
	base := make([]any, 0, len(attrs)+2)
	base = append(base, "operation", operation)
	base = append(base, attrs...)
	slog.InfoContext(ctx, "store operation started", base...)

	return func(err error, results ...any) {
		fields := make([]any, 0, len(base)+2+len(results)*2)
		fields = append(fields, base...)
		fields = append(fields, "duration_ms", time.Since(started).Milliseconds())
		for i, result := range results {
			fields = append(fields, fmt.Sprintf("result_%d", i+1), storeLogResult(result))
		}
		if err != nil {
			fields = append(fields, "error", err)
			slog.ErrorContext(ctx, "store operation failed", fields...)
			return
		}
		slog.InfoContext(ctx, "store operation succeeded", fields...)
	}
}

func storeFinishPanic(finish func(error, ...any), recovered any, results ...any) {
	finish(fmt.Errorf("panic: %v", recovered), results...)
}

// beginStoreTransaction records a multi-statement transaction boundary. The
// caller passes the eventual commit error; deferred rollback remains visible in
// the pgx trace when a transaction exits before commit.
func beginStoreTransaction(ctx context.Context, operation string, attrs ...any) func(error, bool) {
	started := time.Now()
	base := make([]any, 0, len(attrs)+2)
	base = append(base, "operation", operation)
	base = append(base, attrs...)
	slog.InfoContext(ctx, "store transaction started", base...)
	return func(err error, committed bool) {
		fields := append(append([]any{}, base...), "duration_ms", time.Since(started).Milliseconds(), "committed", committed)
		if err != nil {
			fields = append(fields, "error", err)
			slog.ErrorContext(ctx, "store transaction failed", fields...)
			return
		}
		if !committed {
			slog.InfoContext(ctx, "store transaction rolled back without error", fields...)
			return
		}
		slog.InfoContext(ctx, "store transaction committed", fields...)
	}
}

// storeLogValue keeps typed IDs and useful scalar arguments readable while
// avoiding pointer addresses in structured logs.
func storeLogValue(value any) any {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		return rv.Elem().Interface()
	}
	return value
}

// storeLogResult summarizes arbitrary method results without dumping whole
// rows or payloads. Scalars retain their value; collections report their size;
// returned entities report common identifiers when available.
func storeLogResult(result any) any {
	if result == nil {
		return nil
	}
	rv := reflect.ValueOf(result)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if stringer, ok := rv.Interface().(fmt.Stringer); ok {
		return stringer.String()
	}
	switch rv.Kind() {
	case reflect.String:
		return rv.Interface()
	case reflect.Slice, reflect.Array, reflect.Map:
		return map[string]any{"count": rv.Len()}
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return rv.Interface()
	case reflect.Struct:
		for _, name := range []string{"ID", "ReviewID", "ScenarioID", "InstallationID", "RepoID"} {
			field := rv.FieldByName(name)
			if field.IsValid() && field.CanInterface() {
				return map[string]any{"type": rv.Type().String(), "id": field.Interface()}
			}
		}
		return map[string]any{"type": rv.Type().String()}
	default:
		return map[string]any{"type": rv.Type().String()}
	}
}
