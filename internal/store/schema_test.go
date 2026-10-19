package store

import (
	"database/sql"
	"os"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func specSQLBlocks(t *testing.T) string {
	t.Helper()
	spec, err := os.ReadFile("../../docs/specs/data-model.md")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile("(?s)```sql\n(.*?)```").FindAllStringSubmatch(string(spec), 4)
	var b strings.Builder
	for _, m := range blocks {
		b.WriteString(m[1])
		b.WriteString("\n")
	}
	return b.String()
}

func TestDDLMatchesSpecVerbatim(t *testing.T) {
	if DDL != specSQLBlocks(t) {
		t.Fatal("schema.sql drifted from the SQL blocks of docs/specs/data-model.md")
	}
}

func TestDDLAppliesToEmptyDatabase(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/n.db?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(DDL); err != nil {
		t.Fatal(err)
	}
	for _, fts := range []string{"observations_fts", "prompts_fts"} {
		if _, err := db.Exec("INSERT INTO " + fts + "(" + fts + ") VALUES('integrity-check')"); err != nil {
			t.Fatalf("%s: %v", fts, err)
		}
	}
}
