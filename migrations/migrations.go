// Package migrations embeds database migrations into the migration binary.
package migrations

import "embed"

// Files contains every forward-only SQL migration in filename order.
//
//go:embed *.sql
var Files embed.FS
