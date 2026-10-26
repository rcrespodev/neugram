package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var relID = regexp.MustCompile(`^rel-[0-9a-f]{32}$`)

func countRelations(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM memory_relations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func savePair(t *testing.T, s *Store, c *clock) (first, second SaveResult) {
	t.Helper()
	first = save(t, s, NewObservation{Title: "alpha beta", Content: "one"})
	c.advance(time.Minute)
	second = save(t, s, NewObservation{Title: "alpha beta", Content: "two"})
	return first, second
}

func TestSaveCreatesPendingRelationPerCandidate(t *testing.T) {
	s, c := newTestStore(t)
	first, second := savePair(t, s, c)
	if len(second.Candidates) != 1 || !relID.MatchString(second.Candidates[0].JudgmentID) {
		t.Fatalf("want one candidate with rel- judgment_id, got %+v", second.Candidates)
	}
	var source, target, relation, status, session string
	err := s.db.QueryRow(`SELECT source_id, target_id, relation, judgment_status, session_id
		FROM memory_relations WHERE sync_id = ?`, second.Candidates[0].JudgmentID).
		Scan(&source, &target, &relation, &status, &session)
	if err != nil {
		t.Fatal(err)
	}
	if source != second.Observation.SyncID || target != first.Observation.SyncID ||
		relation != "pending" || status != "pending" || session != "sess-1" {
		t.Fatalf("bad pending row: %s %s %s %s %s", source, target, relation, status, session)
	}
}

func TestUpsertReusesPendingRelation(t *testing.T) {
	s, c := newTestStore(t)
	save(t, s, NewObservation{Title: "alpha beta", Content: "one"})
	c.advance(time.Minute)
	a := save(t, s, NewObservation{Title: "alpha beta", Content: "two", TopicKey: "k"})
	b := save(t, s, NewObservation{Title: "alpha beta", Content: "three", TopicKey: "k"})
	if len(a.Candidates) != 1 || len(b.Candidates) != 1 ||
		a.Candidates[0].JudgmentID != b.Candidates[0].JudgmentID || countRelations(t, s) != 1 {
		t.Fatalf("pending relation duplicated: %+v %+v", a.Candidates, b.Candidates)
	}
}

func TestDedupeCreatesNoRelations(t *testing.T) {
	s, c := newTestStore(t)
	savePair(t, s, c)
	before := countRelations(t, s)
	again := save(t, s, NewObservation{Title: "alpha beta", Content: "two"})
	if !again.Deduped || countRelations(t, s) != before {
		t.Fatalf("dedupe touched relations: %+v", again)
	}
}

func TestRelationsRollBackWithObservation(t *testing.T) {
	s, c := newTestStore(t)
	for _, content := range []string{"one", "two", "three"} {
		save(t, s, NewObservation{Title: "alpha beta", Content: content})
		c.advance(time.Minute)
	}
	relations := countRelations(t, s)
	var observations int
	s.db.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&observations)
	_, err := s.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_second BEFORE INSERT ON memory_relations
		WHEN (SELECT COUNT(*) FROM memory_relations) >= %d
		BEGIN SELECT RAISE(ABORT, 'boom'); END`, relations+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddObservation(context.Background(), NewObservation{
		SessionID: "sess-1", Project: "neugram", Type: "decision", Title: "alpha beta", Content: "four",
	}); err == nil {
		t.Fatal("want the save to fail")
	}
	var after int
	s.db.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&after)
	if countRelations(t, s) != relations || after != observations {
		t.Fatalf("save left rows behind: relations %d->%d observations %d->%d", relations, countRelations(t, s), observations, after)
	}
}

func TestPendingReusedAcrossDirections(t *testing.T) {
	s, c := newTestStore(t)
	first, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	cands, err := reuseFor(s, first.Observation.SyncID, second.Observation.SyncID)
	if err != nil {
		t.Fatal(err)
	}
	if cands[0].JudgmentID != id || countRelations(t, s) != 1 {
		t.Fatalf("pair not reused: %+v relations=%d", cands, countRelations(t, s))
	}
}

func reuseFor(s *Store, source, target string) ([]Candidate, error) {
	var out []Candidate
	err := s.withTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		out, err = createPendingRelations(context.Background(), tx, s.timestamp(), "sess-1", source, []Candidate{{SyncID: target}})
		return err
	})
	return out, err
}

func TestJudgedPairIsNotReopened(t *testing.T) {
	s, c := newTestStore(t)
	first, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	if _, err := judge(s, id, "compatible"); err != nil {
		t.Fatal(err)
	}
	cands, err := reuseFor(s, first.Observation.SyncID, second.Observation.SyncID)
	if err != nil {
		t.Fatal(err)
	}
	if cands[0].JudgmentID != id || cands[0].Relation != "compatible" || cands[0].JudgmentStatus != StatusJudged {
		t.Fatalf("judged pair not reported: %+v", cands[0])
	}
}

func TestJudgedPairNotReopenedByUpsert(t *testing.T) {
	s, c := newTestStore(t)
	save(t, s, NewObservation{Title: "alpha beta", Content: "one"})
	c.advance(time.Minute)
	a := save(t, s, NewObservation{Title: "alpha beta", Content: "two", TopicKey: "k"})
	if _, err := judge(s, a.Candidates[0].JudgmentID, "related"); err != nil {
		t.Fatal(err)
	}
	before := countRelations(t, s)
	b := save(t, s, NewObservation{Title: "alpha beta", Content: "three", TopicKey: "k"})
	got := b.Candidates[0]
	if countRelations(t, s) != before || got.JudgmentID != a.Candidates[0].JudgmentID ||
		got.Relation != "related" || got.JudgmentStatus != StatusJudged {
		t.Fatalf("judged pair reopened: %+v relations %d->%d", got, before, countRelations(t, s))
	}
}

func TestJudgeSupersededPointsToHead(t *testing.T) {
	s, c := newTestStore(t)
	_, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	if _, err := judge(s, id, "related"); err != nil {
		t.Fatal(err)
	}
	head, err := judge(s, id, "scoped")
	if err != nil {
		t.Fatal(err)
	}
	_, err = judge(s, id, "supersedes")
	if !errors.Is(err, ErrInvalidRequest) || !strings.Contains(err.Error(), head.JudgmentID) {
		t.Fatalf("want invalid_request naming %s, got %v", head.JudgmentID, err)
	}
}

func TestJudgeRequiredAndRange(t *testing.T) {
	s, c := newTestStore(t)
	_, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	for _, bad := range []float64{-0.1, 1.1} {
		_, err := s.JudgeRelation(context.Background(), JudgmentInput{JudgmentID: id, Relation: "related", SessionID: "sess-1", Confidence: &bad})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("confidence %v: got %v", bad, err)
		}
	}
	for _, in := range []JudgmentInput{
		{Relation: "related", SessionID: "sess-1"},
		{JudgmentID: id, Relation: "related"},
	} {
		if _, err := s.JudgeRelation(context.Background(), in); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("%+v: got %v", in, err)
		}
	}
}

func judge(s *Store, id, relation string) (Judgment, error) {
	conf := 0.9
	return s.JudgeRelation(context.Background(), JudgmentInput{
		JudgmentID: id, Relation: relation, Reason: "r", Evidence: "e", Confidence: &conf, SessionID: "sess-1",
	})
}

func TestJudgeRelation(t *testing.T) {
	s, c := newTestStore(t)
	first, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	c.advance(time.Minute)
	j, err := judge(s, id, "supersedes")
	if err != nil {
		t.Fatal(err)
	}
	if j.JudgmentID != id || j.SourceSyncID != second.Observation.SyncID || j.TargetSyncID != first.Observation.SyncID ||
		j.Relation != "supersedes" || j.JudgmentStatus != "judged" || j.Confidence == nil || *j.Confidence != 0.9 ||
		j.SessionID != "sess-1" || j.SupersededRelationID != "" {
		t.Fatalf("bad judgment: %+v", j)
	}
}

func TestRejudgeSupersedesPreviousJudgment(t *testing.T) {
	s, c := newTestStore(t)
	_, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	if _, err := judge(s, id, "related"); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	j, err := judge(s, id, "supersedes")
	if err != nil {
		t.Fatal(err)
	}
	if j.SupersededRelationID != id || j.JudgmentID == id || !relID.MatchString(j.JudgmentID) || j.JudgmentStatus != "judged" {
		t.Fatalf("bad rejudgment: %+v", j)
	}
	var status, supersededAt string
	var by, newPK int64
	err = s.db.QueryRow(`SELECT judgment_status, superseded_at, superseded_by_relation_id
		FROM memory_relations WHERE sync_id = ?`, id).Scan(&status, &supersededAt, &by)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM memory_relations WHERE sync_id = ?`, j.JudgmentID).Scan(&newPK); err != nil {
		t.Fatal(err)
	}
	if status != "superseded" || supersededAt == "" || by != newPK {
		t.Fatalf("old row not superseded: %s %s %d", status, supersededAt, by)
	}
}

func TestJudgeRelationErrors(t *testing.T) {
	s, c := newTestStore(t)
	_, second := savePair(t, s, c)
	id := second.Candidates[0].JudgmentID
	if _, err := judge(s, id, "pending"); !errors.Is(err, ErrInvalidEnum) {
		t.Fatalf("want invalid_enum, got %v", err)
	}
	if _, err := judge(s, "rel-00000000000000000000000000000000", "related"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not_found, got %v", err)
	}
	_, err := s.JudgeRelation(context.Background(), JudgmentInput{JudgmentID: id, Relation: "related", SessionID: "nope"})
	if !errors.Is(err, ErrUnknownSession) {
		t.Fatalf("want unknown_session, got %v", err)
	}
}
