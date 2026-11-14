// Package migrations embeds the versioned SQL migrations applied by goose.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
