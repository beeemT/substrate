// Package migrations embeds the SQL migration files for Substrate.
package migrations

import "embed"

// FS contains the embedded migration SQL files.
//
//go:embed *.sql
var FS embed.FS
