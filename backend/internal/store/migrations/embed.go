// Package migrations embeds the SQL migration files so they can be bundled into any Go
// binary (cmd/argus, cmd/migrate) and applied via golang-migrate's iofs source without
// needing the .sql files on disk at runtime.
package migrations

import "embed"

// FS contains all *.sql migration files in this directory. The iofs source driver in
// golang-migrate uses the file names to order migrations, so keep the NNN_ prefix.
//
// The glob is intentionally SQL-only. If a future migration needs non-SQL assets (e.g. a
// JSON seed or a CSV import), extend the embed directive with additional patterns.
//
//go:embed *.sql
var FS embed.FS
