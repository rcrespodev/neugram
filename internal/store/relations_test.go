package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
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

func TestRelationsRollBackWithTransaction(t *testing.T) {
	s, c := newTestStore(t)
	first, second := savePair(t, s, c)
	before := countRelations(t, s)
	boom := errors.New("boom")
	err := s.withTx(context.Background(), func(tx *sql.Tx) error {
		cands := []Candidate{{SyncID: first.Observation.SyncID}}
		if _, err := createPendingRelations(context.Background(), tx, s.timestamp(), "sess-1", second.Observation.SyncID, cands); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || countRelations(t, s) != before {
		t.Fatalf("relation survived rollback: err=%v", err)
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
