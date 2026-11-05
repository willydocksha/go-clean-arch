// package migrations embeds the sql files so the binary can migrate without extra tools
package migrations

import "embed"

// all migration files, named <version>_<name>.<up|down>.sql
//
//go:embed *.sql
var FS embed.FS
