package migrations

import "embed"

// FS contains the ordered SQL migrations applied by the store.
//
//go:embed *.sql
var FS embed.FS
