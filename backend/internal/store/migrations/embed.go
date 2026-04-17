// Package migrations embeds the SQL migration files so they can be bundled into any Go
// binary (cmd/argus, cmd/migrate) and applied via golang-migrate's iofs source without
// needing the .sql files on disk at runtime.
package migrations

import "embed"

// FS contains all *.sql migration files in this directory. The iofs source driver in
// golang-migrate uses the file names to order migrations, so keep the NNN_ prefix.
//
//go:embed *.sql
var FS embed.FS
