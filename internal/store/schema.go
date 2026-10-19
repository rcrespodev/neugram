package store

import _ "embed"

// DDL is the full schema, extracted verbatim from the SQL blocks of docs/specs/data-model.md.
//
//go:embed schema.sql
var DDL string
