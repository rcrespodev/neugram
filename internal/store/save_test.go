package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func pinned(t *testing.T, s *Store, key, title string) {
	t.Helper()
	mustCreateTask(t, s, NewTask{Key: key, Title: title})
	if _, err := s.PinSession(context.Background(), PinRequest{SessionID: "sess-1", Target: key}); err != nil {
		t.Fatal(err)
	}
}

func add(s *Store, in NewObservation) (SaveResult, error) {
	in.SessionID, in.Type, in.Title = "sess-1", "decision", "titulo"
	return s.AddObservation(context.Background(), in)
}

func TestSaveWithoutTaskNorPinFailsWithOpenTasks(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreateTask(t, s, NewTask{Key: "T-1", Title: "abierta"})
	_, err := add(s, NewObservation{Content: "cuerpo"})
	wantCode(t, err, ErrNoPin)
	var e *Error
	if !errors.As(err, &e) || e.Hint == "" || !slices.Equal(e.Details["open_tasks"].([]string), []string{"T-1 — abierta"}) {
		t.Fatalf("%+v", e)
	}
}

func TestSaveWithPinAddsFooter(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	res, err := add(s, NewObservation{Content: "cuerpo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Observation.Content != "cuerpo\n\n**Task**: T-1 — Una tarea" || res.FooterRewritten || res.TaskKey != "T-1" || res.Observation.TaskID == "" {
		t.Fatalf("%+v", res)
	}
	if res.Observation.Project != "neugram" {
		t.Fatalf("project %q", res.Observation.Project)
	}
}

func TestSaveCorrectsFooterOfAnotherTask(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	res, err := add(s, NewObservation{Content: "cuerpo\n\n**Task**: OTRA-9 — Otra"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.FooterRewritten || res.Observation.Content != "cuerpo\n\n**Task**: T-1 — Una tarea" {
		t.Fatalf("%+v", res)
	}
	ok, err := add(s, NewObservation{Content: "otro\n\n**Task**: t-1 — título viejo"})
	if err != nil || ok.FooterRewritten || !strings.HasSuffix(ok.Observation.Content, "título viejo") {
		t.Fatalf("%+v %v", ok, err)
	}
}

func TestSaveWithNonePinKeepsNingunaFooter(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.PinSession(context.Background(), PinRequest{SessionID: "sess-1", Target: "none"}); err != nil {
		t.Fatal(err)
	}
	kept, err := add(s, NewObservation{Content: "a\n\n**Task**: ninguna — setup"})
	if err != nil || kept.FooterRewritten || kept.Observation.Content != "a\n\n**Task**: ninguna — setup" || kept.Observation.TaskID != "" {
		t.Fatalf("%+v %v", kept, err)
	}
	added, err := add(s, NewObservation{Content: "b"})
	if err != nil || added.Observation.Content != "b\n\n**Task**: ninguna" {
		t.Fatalf("%+v %v", added, err)
	}
}

func TestRepeatedSaveDedupes(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	first, _ := add(s, NewObservation{Content: "cuerpo"})
	again, err := add(s, NewObservation{Content: "cuerpo"})
	if err != nil || !again.Deduped || again.Observation.SyncID != first.Observation.SyncID {
		t.Fatalf("%+v %v", again, err)
	}
	withFooter, err := add(s, NewObservation{Content: "cuerpo\n\n**Task**: T-1 — Una tarea"})
	if err != nil || !withFooter.Deduped {
		t.Fatalf("%+v %v", withFooter, err)
	}
	wrongFooter, err := add(s, NewObservation{Content: "cuerpo\n\n**Task**: X-1 — mal"})
	if err != nil || !wrongFooter.Deduped {
		t.Fatalf("%+v %v", wrongFooter, err)
	}
}

func TestSaveUnknownSession(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.AddObservation(context.Background(), NewObservation{SessionID: "nope", Type: "decision", Title: "t", Content: "c", TaskID: "none"})
	wantCode(t, err, ErrUnknownSession)
	_, err = s.SaveHandoff(context.Background(), Handoff{SessionID: "nope", Content: "c", TaskID: "none"})
	wantCode(t, err, ErrUnknownSession)
}

func TestHandoffTwiceKeepsOneRow(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	ctx := context.Background()
	first, err := s.SaveHandoff(ctx, Handoff{SessionID: "sess-1", Content: "## State\nvamos por la mitad"})
	if err != nil || first.Replaced || first.Observation.Kind != KindHandoff || first.Observation.Type != "handoff" {
		t.Fatalf("%+v %v", first, err)
	}
	if first.Observation.Title != "Handoff T-1: vamos por la mitad" || !strings.HasSuffix(first.Observation.Content, "**Task**: T-1 — Una tarea") {
		t.Fatalf("%+v", first.Observation)
	}
	second, err := s.SaveHandoff(ctx, Handoff{SessionID: "sess-1", Content: "## State\nterminado"})
	if err != nil || !second.Replaced || second.Observation.SyncID != first.Observation.SyncID || second.Observation.RevisionCount != 2 {
		t.Fatalf("%+v %v", second, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM observations WHERE session_id = 'sess-1' AND kind = 'handoff'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows %d %v", n, err)
	}
}

func TestHandoffWithoutPinFails(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.SaveHandoff(context.Background(), Handoff{SessionID: "sess-1", Content: "c"})
	wantCode(t, err, ErrNoPin)
}

func TestUpdateTaskIDRewritesFooter(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	mustCreateTask(t, s, NewTask{Key: "T-2", Title: "Otra tarea"})
	res, _ := add(s, NewObservation{Content: "cuerpo"})
	task := "T-2"
	up, err := s.UpdateObservation(context.Background(), res.Observation.SyncID, ObservationPatch{TaskID: &task})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(up.Changed, []string{"task_id", "content"}) || !up.FooterRewritten || up.TaskKey != "T-2" ||
		up.Observation.Content != "cuerpo\n\n**Task**: T-2 — Otra tarea" || up.Observation.NormalizedHash != NormalizedHash(up.Observation.Content) {
		t.Fatalf("%+v", up)
	}
	none := "none"
	up, err = s.UpdateObservation(context.Background(), res.Observation.SyncID, ObservationPatch{TaskID: &none})
	if err != nil || up.Observation.TaskID != "" || !strings.HasSuffix(up.Observation.Content, "**Task**: ninguna") {
		t.Fatalf("%+v %v", up, err)
	}
	bad := "X-404"
	_, err = s.UpdateObservation(context.Background(), res.Observation.SyncID, ObservationPatch{TaskID: &bad})
	wantCode(t, err, ErrUnknownTask)
}
