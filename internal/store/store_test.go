package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "neugram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	s.now = c.now
	if _, _, err := s.UpsertSession(context.Background(), "sess-1", "neugram", "/repo/neugram"); err != nil {
		t.Fatal(err)
	}
	return s, c
}

func save(t *testing.T, s *Store, in NewObservation) SaveResult {
	t.Helper()
	if in.SessionID == "" {
		in.SessionID = "sess-1"
	}
	if in.Project == "" {
		in.Project = "neugram"
	}
	if in.Type == "" {
		in.Type = "decision"
	}
	res, err := s.AddObservation(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func search(t *testing.T, s *Store, p SearchParams) ([]SearchResult, int) {
	t.Helper()
	got, total, err := s.SearchObservations(context.Background(), p)
	if err != nil {
		t.Fatalf("%+v: %v", p, err)
	}
	return got, total
}

func listPrompts(t *testing.T, s *Store, q PromptQuery) ([]Prompt, int) {
	t.Helper()
	got, total, err := s.ListPrompts(context.Background(), q)
	if err != nil {
		t.Fatalf("%+v: %v", q, err)
	}
	return got, total
}

func addPrompt(t *testing.T, s *Store, content, project string) Prompt {
	t.Helper()
	p, deduped, err := s.AddPrompt(context.Background(), "sess-1", content, project)
	if err != nil {
		t.Fatal(err)
	}
	if deduped {
		t.Fatalf("%q unexpectedly deduped", content)
	}
	return p
}

func softDelete(t *testing.T, s *Store, syncID string) {
	t.Helper()
	if err := s.DeleteObservation(context.Background(), syncID); err != nil {
		t.Fatal(err)
	}
}

func queryValue[T any](t *testing.T, db *sql.DB, query string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func assertFTSIntegrity(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO observations_fts(observations_fts) VALUES('integrity-check')`,
		`INSERT INTO observations_fts(observations_fts, rank) VALUES('integrity-check', 1)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestOpenCreatesSchemaOnceAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "neugram.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := queryValue[string](t, s.db, `PRAGMA journal_mode`); mode != "wal" {
		t.Fatalf("journal_mode=%s", mode)
	}
	if fk := queryValue[int](t, s.db, `PRAGMA foreign_keys`); fk != 1 {
		t.Fatalf("foreign_keys=%d", fk)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := queryValue[string](t, s.db, `SELECT value FROM meta WHERE key = 'schema_version'`); v != SchemaVersion {
		t.Fatalf("schema_version=%q", v)
	}
}

func TestOpenHandlesPathsThatNeedEscaping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dir with space?#%", "neu gram.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database not at the requested path: %v", err)
	}
}

func TestOpenConcurrentFirstOpenAppliesSchemaOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "race.db")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	stores := make([]*Store, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = Open(path)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		defer stores[i].Close()
	}
	if n := queryValue[int](t, stores[0].db, `SELECT count(*) FROM meta WHERE key = 'schema_version'`); n != 1 {
		t.Fatalf("schema_version rows: %d", n)
	}
}

func TestOpenRejectsForeignDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE x (a)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("expected error opening a database without meta")
	}
}

func TestDefaultPathIsUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(home, ".neugram", "neugram.db") {
		t.Fatalf("got %s", p)
	}
}

func TestFTSStaysConsistentAcrossInsertUpdateDelete(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := save(t, s, NewObservation{Title: "FTS triggers verbatim", Content: "external content table"})
	assertFTSIntegrity(t, s)

	content := "rewritten body with zebra"
	if _, _, err := s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{Content: &content}); err != nil {
		t.Fatal(err)
	}
	assertFTSIntegrity(t, s)
	if got, _ := search(t, s, SearchParams{Query: "zebra"}); len(got) != 1 {
		t.Fatalf("updated content not indexed: %d results", len(got))
	}
	if got, _ := search(t, s, SearchParams{Query: "external"}); len(got) != 0 {
		t.Fatalf("stale content still indexed: %d results", len(got))
	}

	if _, err := s.db.Exec(`DELETE FROM observations WHERE sync_id = ?`, res.Observation.SyncID); err != nil {
		t.Fatal(err)
	}
	assertFTSIntegrity(t, s)
	if n := queryValue[int](t, s.db, `SELECT count(*) FROM observations_fts WHERE observations_fts MATCH 'zebra'`); n != 0 {
		t.Fatalf("ghost row after DELETE: %d", n)
	}
}

func TestAddObservationInsertsWithPublicSyncID(t *testing.T) {
	s, _ := newTestStore(t)
	res := save(t, s, NewObservation{Title: "Store opens WAL", Content: "Body"})
	o := res.Observation
	if len(o.SyncID) != len("obs-")+32 || o.SyncID[:4] != "obs-" {
		t.Fatalf("sync_id %q", o.SyncID)
	}
	if o.Scope != ScopeProject || o.RevisionCount != 1 || o.DuplicateCount != 1 || o.CreatedAt != "2026-10-19T12:00:00Z" {
		t.Fatalf("unexpected row %+v", o)
	}
	if o.NormalizedHash != NormalizedHash("Body") || res.Deduped || res.Upserted {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestAddObservationValidates(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	cases := []struct {
		in   NewObservation
		want error
	}{
		{NewObservation{SessionID: "sess-1", Project: "p", Type: "bug", Title: "t", Content: "c"}, ErrInvalidType},
		{NewObservation{SessionID: "sess-1", Project: "p", Type: "decision", Scope: "team", Title: "t", Content: "c"}, ErrInvalidEnum},
		{NewObservation{SessionID: "sess-1", Project: "p", Type: "decision", Kind: "note", Title: "t", Content: "c"}, ErrInvalidEnum},
		{NewObservation{SessionID: "sess-1", Project: "p", Type: "decision", Title: "", Content: "c"}, ErrInvalidRequest},
		{NewObservation{SessionID: "nope", Project: "p", Type: "decision", Title: "t", Content: "c"}, ErrUnknownSession},
	}
	for _, c := range cases {
		if _, err := s.AddObservation(ctx, c.in); !errors.Is(err, c.want) {
			t.Errorf("%+v: got %v, want %v", c.in, err, c.want)
		}
	}
}

func TestDedupeWithinWindowBumpsDuplicateCount(t *testing.T) {
	s, c := newTestStore(t)
	first := save(t, s, NewObservation{Title: "Same", Content: "Hello   World"})
	c.advance(10 * time.Minute)
	second := save(t, s, NewObservation{Title: "Same", Content: "hello world"})
	if !second.Deduped || second.Observation.SyncID != first.Observation.SyncID {
		t.Fatalf("expected dedupe onto %s, got %+v", first.Observation.SyncID, second)
	}
	if second.Observation.DuplicateCount != 2 || second.Observation.LastSeenAt != "2026-10-19T12:10:00Z" {
		t.Fatalf("unexpected row %+v", second.Observation)
	}
}

func TestDedupeRequiresSameKeyAndWindow(t *testing.T) {
	s, c := newTestStore(t)
	base := save(t, s, NewObservation{Title: "Same", Content: "body"})
	others := []NewObservation{
		{Title: "Other title", Content: "body"},
		{Title: "Same", Content: "body", Type: "bugfix"},
		{Title: "Same", Content: "body", Scope: ScopePersonal},
		{Title: "Same", Content: "body", Project: "other"},
	}
	for _, in := range others {
		if res := save(t, s, in); res.Deduped {
			t.Errorf("%+v deduped onto %s", in, base.Observation.SyncID)
		}
	}
	c.advance(DedupeWindow + time.Second)
	if res := save(t, s, NewObservation{Title: "Same", Content: "body"}); res.Deduped {
		t.Fatal("deduped outside the window")
	}
}

func TestDedupeSkipsCandidates(t *testing.T) {
	s, c := newTestStore(t)
	save(t, s, NewObservation{Title: "alpha beta", Content: "one"})
	c.advance(time.Minute)
	second := save(t, s, NewObservation{Title: "alpha beta", Content: "two"})
	if len(second.Candidates) != 1 {
		t.Fatalf("setup: want one candidate, got %+v", second.Candidates)
	}
	again := save(t, s, NewObservation{Title: "alpha beta", Content: "two"})
	if !again.Deduped || len(again.Candidates) != 0 {
		t.Fatalf("dedupe ran candidates: %+v", again)
	}
}

func TestTopicKeyUpsertBumpsRevisionCount(t *testing.T) {
	s, c := newTestStore(t)
	first := save(t, s, NewObservation{Title: "Auth model v1", Content: "one", TopicKey: "task/X/auth"})
	c.advance(time.Hour)
	second := save(t, s, NewObservation{Title: "Auth model v2", Content: "two", TopicKey: "task/X/auth"})
	if !second.Upserted || second.Observation.SyncID != first.Observation.SyncID {
		t.Fatalf("expected upsert onto %s, got %+v", first.Observation.SyncID, second)
	}
	o := second.Observation
	if o.RevisionCount != 2 || o.Content != "two" || o.Title != "Auth model v2" || o.UpdatedAt != "2026-10-19T13:00:00Z" {
		t.Fatalf("unexpected row %+v", o)
	}
	personal := save(t, s, NewObservation{Title: "Auth model", Content: "three", TopicKey: "task/X/auth", Scope: ScopePersonal})
	if personal.Upserted || personal.Observation.SyncID == first.Observation.SyncID {
		t.Fatal("upsert crossed scopes")
	}
}

func TestTopicKeyNeverDedupes(t *testing.T) {
	s, _ := newTestStore(t)
	plain := save(t, s, NewObservation{Title: "Same", Content: "body"})
	keyed := save(t, s, NewObservation{Title: "Same", Content: "body", TopicKey: "k"})
	if keyed.Deduped || keyed.Observation.SyncID == plain.Observation.SyncID || keyed.Observation.TopicKey != "k" {
		t.Fatalf("topic_key save went through dedupe: %+v", keyed)
	}
}

func TestTopicKeyUpsertSkipsSoftDeletedRow(t *testing.T) {
	s, _ := newTestStore(t)
	first := save(t, s, NewObservation{Title: "v1", Content: "one", TopicKey: "k"})
	softDelete(t, s, first.Observation.SyncID)
	second := save(t, s, NewObservation{Title: "v2", Content: "two", TopicKey: "k"})
	if second.Upserted || second.Observation.SyncID == first.Observation.SyncID || second.Observation.RevisionCount != 1 {
		t.Fatalf("upsert revived a deleted row: %+v", second)
	}
	if _, err := s.GetObservation(context.Background(), first.Observation.SyncID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted row visible: %v", err)
	}
}

func TestUpdateObservation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := save(t, s, NewObservation{Title: "t", Content: "c", TopicKey: "k"})
	content, clear := "New Content", ""
	o, changed, err := s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{Content: &content, TopicKey: &clear})
	if err != nil {
		t.Fatal(err)
	}
	if o.RevisionCount != 2 || o.TopicKey != "" || o.NormalizedHash != NormalizedHash(content) {
		t.Fatalf("unexpected row %+v", o)
	}
	if len(changed) != 2 || changed[0] != "content" || changed[1] != "topic_key" {
		t.Fatalf("changed=%v", changed)
	}
	if _, _, err := s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty patch: %v", err)
	}
	bad := "nope"
	if _, _, err := s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{Type: &bad}); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("bad type: %v", err)
	}
	if _, _, err := s.UpdateObservation(ctx, "obs-missing", ObservationPatch{Content: &content}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestSoftDeleteHidesFromEveryRead(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := save(t, s, NewObservation{Title: "Platypus note", Content: "platypus"})
	id := res.Observation.SyncID
	softDelete(t, s, id)
	if _, err := s.GetObservation(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
	if got, total := search(t, s, SearchParams{Query: "platypus"}); len(got) != 0 || total != 0 {
		t.Fatalf("search after delete: %d/%d", len(got), total)
	}
	if got, _ := search(t, s, SearchParams{}); len(got) != 0 {
		t.Fatalf("list after delete: %d", len(got))
	}
	content := "x"
	if _, _, err := s.UpdateObservation(ctx, id, ObservationPatch{Content: &content}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update after delete: %v", err)
	}
	if err := s.DeleteObservation(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if res := save(t, s, NewObservation{Title: "Platypus note", Content: "platypus"}); res.Deduped {
		t.Fatal("deduped onto a deleted row")
	}
	assertFTSIntegrity(t, s)
}

func TestFTSQuery(t *testing.T) {
	cases := []struct{ in, mode, want string }{
		{`"fix auth"bug  -p`, MatchAll, `"fix" "authbug" "-p"`},
		{`"fix auth"bug  -p`, MatchAny, `"fix" OR "authbug" OR "-p"`},
		{`title:foo NEAR(a b) bar*`, "", `"title:foo" "NEAR(a" "b)" "bar*"`},
	}
	for _, c := range cases {
		got, err := FTSQuery(c.in, c.mode)
		if err != nil || got != c.want {
			t.Errorf("FTSQuery(%q, %q) = %q, %v; want %q", c.in, c.mode, got, err, c.want)
		}
	}
	for _, in := range []string{"", "   ", `"" """`} {
		if _, err := FTSQuery(in, MatchAll); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("FTSQuery(%q): %v", in, err)
		}
	}
	if _, err := FTSQuery("a", "near"); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("bad mode: %v", err)
	}
}

func TestHostileQueriesNeverError(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: "title: NEAR operators", Content: `content with (parens) and * stars and "quotes"`})
	for _, q := range []string{
		"NEAR", "NEAR(a b)", "title:", "title:NEAR", "*", "(", ")", `a"b`, `"unterminated`,
		"AND", "OR", "NOT", "a OR", "-", "^", "col:val", "{title content}:x", ":", "+", "a AND (b OR",
	} {
		for _, mode := range []string{MatchAll, MatchAny} {
			search(t, s, SearchParams{Query: q, MatchMode: mode})
		}
	}
	if got, _ := search(t, s, SearchParams{Query: "(parens)"}); len(got) != 1 {
		t.Fatalf("literal parens: %d", len(got))
	}
	for _, q := range []string{`"`, `"" ""`} {
		if _, _, err := s.SearchObservations(context.Background(), SearchParams{Query: q}); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("query %q empty after sanitize: %v", q, err)
		}
	}
}

func TestSearchMatchesOnlyTitleAndContent(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: "plain title", Content: "plain body", Type: "architecture", TopicKey: "wombat/key", ToolName: "Bash"})
	for _, q := range []string{"neugram", "architecture", "wombat", "Bash"} {
		for _, mode := range []string{MatchAll, MatchAny} {
			if got, _ := search(t, s, SearchParams{Query: q, MatchMode: mode}); len(got) != 0 {
				t.Errorf("query %q mode %s matched a non-text column: %d", q, mode, len(got))
			}
		}
	}
	if got, _ := search(t, s, SearchParams{Query: "plain"}); len(got) != 1 {
		t.Fatalf("title/content match: %d", len(got))
	}
}

func TestSearchRanksAndFilters(t *testing.T) {
	s, c := newTestStore(t)
	save(t, s, NewObservation{Title: "unrelated", Content: "kiwi once among many many other words in this long body"})
	c.advance(time.Minute)
	best := save(t, s, NewObservation{Title: "kiwi kiwi", Content: "kiwi"})
	c.advance(time.Minute)
	save(t, s, NewObservation{Title: "kiwi elsewhere", Content: "kiwi", Project: "other", Type: "bugfix"})

	got, total := search(t, s, SearchParams{Query: "kiwi"})
	if total != 3 || len(got) != 3 {
		t.Fatalf("total=%d len=%d", total, len(got))
	}
	if got[0].SyncID != best.Observation.SyncID || got[0].Score <= got[1].Score || got[2].Score <= 0 {
		t.Fatalf("ranking: %+v", got)
	}
	got, _ = search(t, s, SearchParams{Query: "kiwi", Project: "other", Types: []string{"bugfix"}})
	if len(got) != 1 || got[0].Project != "other" {
		t.Fatalf("filter: %+v", got)
	}
	got, total = search(t, s, SearchParams{Query: "kiwi", Limit: 1, Offset: 1})
	if len(got) != 1 || total != 3 {
		t.Fatalf("paging: len=%d total=%d", len(got), total)
	}
	got, _ = search(t, s, SearchParams{Since: "2026-10-19T12:01:00Z"})
	if len(got) != 2 || got[0].Project != "other" {
		t.Fatalf("listing: %+v", got)
	}
	if got, _ = search(t, s, SearchParams{Query: "kiwi unrelated", MatchMode: MatchAny}); len(got) != 3 {
		t.Fatalf("any: %d", len(got))
	}
	if got, _ = search(t, s, SearchParams{Query: "kiwi unrelated"}); len(got) != 1 {
		t.Fatalf("all: %d", len(got))
	}
	for _, p := range []SearchParams{{Limit: -1}, {Limit: MaxSearchLimit + 1}, {Offset: -1}} {
		if _, _, err := s.SearchObservations(context.Background(), p); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%+v: %v", p, err)
		}
	}
}

func TestListingBreaksCreatedAtTiesByInsertionOrder(t *testing.T) {
	s, _ := newTestStore(t)
	first := save(t, s, NewObservation{Title: "first", Content: "a"})
	second := save(t, s, NewObservation{Title: "second", Content: "b"})
	got, _ := search(t, s, SearchParams{})
	if len(got) != 2 || got[0].SyncID != second.Observation.SyncID || got[1].SyncID != first.Observation.SyncID {
		t.Fatalf("tie order: %+v", got)
	}
	for _, r := range got {
		if r.Score != 0 || math.Signbit(r.Score) {
			t.Fatalf("listing score %v, want +0", r.Score)
		}
	}
}

func TestFindCandidatesThresholdAndCap(t *testing.T) {
	s, c := newTestStore(t)
	for i := range 5 {
		save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "body " + string(rune('a'+i))})
		c.advance(time.Minute)
	}
	save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "personal", Scope: ScopePersonal})
	save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "other repo", Project: "other"})
	deleted := save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "gone"})
	softDelete(t, s, deleted.Observation.SyncID)

	res := save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "new"})
	if len(res.Candidates) != MaxCandidates {
		t.Fatalf("want %d candidates, got %+v", MaxCandidates, res.Candidates)
	}
	for _, cand := range res.Candidates {
		if cand.SyncID == res.Observation.SyncID || cand.SyncID == deleted.Observation.SyncID || cand.Similarity < CandidateMinSimilarity {
			t.Fatalf("bad candidate %+v", cand)
		}
		if cand.Project != "neugram" || cand.Preview == "" {
			t.Fatalf("bad candidate %+v", cand)
		}
	}

	weak := save(t, s, NewObservation{Title: "tareas pendientes de revisar mañana temprano", Content: "x"})
	if len(weak.Candidates) != 0 {
		t.Fatalf("weak overlap passed the threshold: %+v", weak.Candidates)
	}
	again, err := s.FindCandidates(context.Background(), res.Observation.SyncID)
	if err != nil || len(again) != MaxCandidates {
		t.Fatalf("FindCandidates: %d, %v", len(again), err)
	}
	if _, err := s.FindCandidates(context.Background(), "obs-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestFindCandidatesSimilarityIsRankOverSelfRank(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: "alpha beta", Content: "one"})
	res := save(t, s, NewObservation{Title: "alpha beta", Content: "two"})
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates %+v", res.Candidates)
	}
	if sim := res.Candidates[0].Similarity; sim < 0.99 || sim > 1.01 {
		t.Fatalf("identical titles should give similarity ~1, got %f", sim)
	}
}

func TestFindCandidatesIgnoresNonTextColumns(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: "unrelated words", Content: "x", Type: "architecture"})
	res := save(t, s, NewObservation{Title: "architecture neugram", Content: "y", Type: "architecture"})
	if len(res.Candidates) != 0 {
		t.Fatalf("matched on type/project: %+v", res.Candidates)
	}
}

func TestFindCandidatesWithoutTitleTerms(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: `""`, Content: "one"})
	res := save(t, s, NewObservation{Title: `" "`, Content: "two"})
	if len(res.Candidates) != 0 {
		t.Fatalf("candidates for a title without terms: %+v", res.Candidates)
	}
}

var excludedObservations = []NewObservation{
	{Type: "session_summary"},
	{Type: "handoff", Kind: KindHandoff},
	{Type: "handoff"},
	{Type: "discovery", Kind: KindHandoff},
}

func TestFindCandidatesSkipsExcludedNewObservation(t *testing.T) {
	for _, in := range excludedObservations {
		t.Run(in.Type+"/"+in.Kind, func(t *testing.T) {
			s, _ := newTestStore(t)
			save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "one"})
			in.Title, in.Content = "Jerarquía de tareas", "two"
			res := save(t, s, in)
			if len(res.Candidates) != 0 {
				t.Fatalf("candidates for an excluded observation: %+v", res.Candidates)
			}
			again, err := s.FindCandidates(context.Background(), res.Observation.SyncID)
			if err != nil || len(again) != 0 {
				t.Fatalf("FindCandidates: %+v, %v", again, err)
			}
		})
	}
}

func TestFindCandidatesNeverOffersExcludedRows(t *testing.T) {
	for _, in := range excludedObservations {
		t.Run(in.Type+"/"+in.Kind, func(t *testing.T) {
			s, _ := newTestStore(t)
			kept := save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "jerarquía de tareas con tres niveles y un trigger"})
			in.Title, in.Content = "Jerarquía de tareas", "jerarquía de tareas"
			excluded := save(t, s, in)
			res := save(t, s, NewObservation{Title: "Jerarquía de tareas", Content: "nueva"})
			if len(res.Candidates) != 1 || res.Candidates[0].SyncID != kept.Observation.SyncID {
				t.Fatalf("want only %s, got %+v (excluded %s)", kept.Observation.SyncID, res.Candidates, excluded.Observation.SyncID)
			}
		})
	}
}

func TestSessions(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	c.advance(time.Hour)
	sess, created, err := s.UpsertSession(ctx, "sess-1", "other", "/repo/moved")
	if err != nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if sess.Project != "neugram" || sess.Directory != "/repo/neugram" || sess.StartedAt != "2026-10-19T12:00:00Z" || sess.UpdatedAt != "2026-10-19T12:00:00Z" {
		t.Fatalf("existing session was modified: %+v", sess)
	}
	if err := s.EndSession(ctx, "sess-1"); err != nil {
		t.Fatal(err)
	}
	sess, err = s.GetSession(ctx, "sess-1")
	if err != nil || sess.EndedAt != "2026-10-19T13:00:00Z" {
		t.Fatalf("ended_at %q, %v", sess.EndedAt, err)
	}
	if _, created, err := s.UpsertSession(ctx, "sess-2", "neugram", "/d"); err != nil || !created {
		t.Fatalf("new session: created=%v err=%v", created, err)
	}
	if _, err := s.GetSession(ctx, "missing"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("missing: %v", err)
	}
	if err := s.EndSession(ctx, "missing"); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("end missing: %v", err)
	}
	if _, _, err := s.UpsertSession(ctx, "", "p", "/d"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty id: %v", err)
	}
}

func TestPrompts(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	p := addPrompt(t, s, "quiero definir la API", "")
	if p.Project != "neugram" || p.SyncID[:4] != "prm-" || len(p.SyncID) != 36 {
		t.Fatalf("p=%+v", p)
	}
	c.advance(time.Minute)
	again, deduped, err := s.AddPrompt(ctx, "sess-1", "quiero definir la API", "")
	if err != nil || !deduped || again.SyncID != p.SyncID || again.CreatedAt != p.CreatedAt {
		t.Fatalf("again=%+v deduped=%v err=%v", again, deduped, err)
	}
	if _, _, err := s.UpsertSession(ctx, "sess-2", "neugram", "/d"); err != nil {
		t.Fatal(err)
	}
	other, deduped, err := s.AddPrompt(ctx, "sess-2", "quiero definir la API", "")
	if err != nil || deduped || other.SyncID == p.SyncID {
		t.Fatalf("other session: %+v deduped=%v err=%v", other, deduped, err)
	}
	c.advance(time.Minute)
	addPrompt(t, s, "otra cosa NEAR(", "other")
	if _, _, err := s.AddPrompt(ctx, "missing", "x", ""); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("unknown session: %v", err)
	}
	if _, _, err := s.AddPrompt(ctx, "sess-1", "", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty: %v", err)
	}

	got, total := listPrompts(t, s, PromptQuery{SessionID: "sess-1"})
	if total != 2 || got[0].Content != "otra cosa NEAR(" {
		t.Fatalf("list: %+v total=%d", got, total)
	}
	if got, _ = listPrompts(t, s, PromptQuery{Query: "API"}); len(got) != 2 {
		t.Fatalf("fts: %+v", got)
	}
	if got, _ = listPrompts(t, s, PromptQuery{Query: "NEAR("}); len(got) != 1 {
		t.Fatalf("hostile: %+v", got)
	}
	if got, _ = listPrompts(t, s, PromptQuery{Query: "other"}); len(got) != 0 {
		t.Fatalf("matched the project column: %+v", got)
	}
	if got, _ = listPrompts(t, s, PromptQuery{Project: "other"}); len(got) != 1 {
		t.Fatalf("project: %+v", got)
	}
	if _, _, err := s.ListPrompts(ctx, PromptQuery{SessionID: "missing"}); !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO prompts_fts(prompts_fts, rank) VALUES('integrity-check', 1)`); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizedHash(t *testing.T) {
	want := NormalizedHash("hello world")
	for _, in := range []string{"Hello World", "hello \t\n  world", "HELLO world"} {
		if got := NormalizedHash(in); got != want {
			t.Errorf("NormalizedHash(%q) = %s, want %s", in, got, want)
		}
	}
	if NormalizedHash("hello world") == NormalizedHash("helloworld") {
		t.Error("whitespace removed instead of collapsed")
	}
	if NormalizedHash("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Error("empty content is not sha256 of empty string")
	}
}

func TestNormalizedHashMatchesLegacyDatabase(t *testing.T) {
	path := os.Getenv("NEUGRAM_LEGACY_DB")
	if path == "" {
		t.Skip("NEUGRAM_LEGACY_DB not set")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT sync_id, content, normalized_hash FROM observations WHERE normalized_hash IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n, bad int
	for rows.Next() {
		var id, content, stored string
		if err := rows.Scan(&id, &content, &stored); err != nil {
			t.Fatal(err)
		}
		n++
		if NormalizedHash(content) != stored {
			bad++
			t.Errorf("%s: hash mismatch", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("legacy database has no hashed observations")
	}
	t.Logf("%d/%d legacy hashes reproduced", n-bad, n)
}
