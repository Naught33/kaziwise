// Package migrations embeds the SQL schema so a single compiled binary
// can provision a fresh Supabase database on first boot. The files are
// also on disk in this directory, which is what a developer edits; the
// runner prefers the on-disk copy when it is present.
//
// The split between the two embedded sets is deliberate. Schema belongs in
// every environment; demo content must never be created silently, so the
// seed files live in seeds/ and only run when SEED_DEMO=true.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

//go:embed seeds/*.sql
var SeedFS embed.FS

// Dir is the path prefix of the embedded schema files.
const Dir = "."

// SeedDir is the path prefix of the embedded seed files.
const SeedDir = "seeds"
