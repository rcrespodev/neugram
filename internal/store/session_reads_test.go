package store

import (
	"context"
	"testing"
	"time"
)

func newSession(t *testing.T, s *Store, id, dir string) {
	t.Helper()
	if _, _, err := s.UpsertSession(context.Background(), id, "neugram", dir); err != nil {
		t.Fatal(err)
	}
}

func handoffOf(t *testing.T, s *Store, session, task, content string) {
	t.Helper()
	if _, err := s.SaveHandoff(context.Background(), Handoff{SessionID: session, TaskID: task, Content: content}); err != nil {
		t.Fatal(err)
	}
}

func TestLastHandoffIgnoresAncestors(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, NewTask{Key: "EPIC", Title: "epico"})
	mustCreateTask(t, s, NewTask{Key: "TASK", Title: "tarea", Parent: "EPIC"})
	newSession(t, s, "sess-2", "/repo/neugram")
	handoffOf(t, s, "sess-1", "TASK", "de la tarea")
	c.advance(time.Hour)
	handoffOf(t, s, "sess-2", "EPIC", "del epico")

	got, err := s.LastHandoff(ctx, "TASK")
	if err != nil || got.TaskKey != "TASK" || got.Observation.SessionID != "sess-1" {
		t.Fatalf("%+v %v", got, err)
	}
	mustCreateTask(t, s, NewTask{Key: "EMPTY", Title: "sin handoff", Parent: "EPIC"})
	_, err = s.LastHandoff(ctx, "EMPTY")
	wantCode(t, err, ErrNotFound)
	_, err = s.LastHandoff(ctx, "NOPE")
	wantCode(t, err, ErrUnknownTask)
}

func TestLastHandoffByDirectoryReturnsTheNewest(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	newSession(t, s, "sess-2", "/repo/neugram")
	newSession(t, s, "sess-3", "/otro")
	handoffOf(t, s, "sess-1", NoTaskKey, "viejo")
	c.advance(time.Hour)
	handoffOf(t, s, "sess-2", NoTaskKey, "nuevo")
	c.advance(time.Hour)
	handoffOf(t, s, "sess-3", NoTaskKey, "de otro directorio")

	got, err := s.LastHandoffByDirectory(ctx, "/repo/neugram")
	if err != nil || got.Observation.SessionID != "sess-2" || got.TaskKey != NoTaskKey {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = s.LastHandoffByDirectory(ctx, "/nada")
	wantCode(t, err, ErrNotFound)
}

func TestLastPinnedSessionByDirectory(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, NewTask{Key: "T", Title: "t"})
	newSession(t, s, "sess-2", "/repo/neugram")
	newSession(t, s, "sess-3", "/repo/neugram")
	newSession(t, s, "sess-4", "/repo/neugram")
	for _, p := range []PinRequest{{SessionID: "sess-1", Target: NoTaskKey}, {SessionID: "sess-2", Target: "T"}} {
		c.advance(time.Hour)
		if _, err := s.PinSession(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastPinnedSessionByDirectory(ctx, "/repo/neugram", "sess-4")
	if err != nil || got.Session.ID != "sess-2" || got.TaskKey != "T" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = s.LastPinnedSessionByDirectory(ctx, "/repo/neugram", "sess-2")
	if err != nil || got.Session.ID != "sess-1" || got.TaskKey != NoTaskKey {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = s.LastPinnedSessionByDirectory(ctx, "/nada", "sess-4")
	wantCode(t, err, ErrNotFound)
}

func TestTaskWorktreesReportDeadPaths(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, NewTask{Key: "T", Title: "t"})
	live := t.TempDir()
	for _, p := range []string{live, live + "/borrado"} {
		if _, err := s.UpsertWorktree(ctx, NewWorktree{Path: p, Repo: "r", Branch: "b", Task: "T"}); err != nil {
			t.Fatal(err)
		}
	}
	got, total, err := s.ListWorktrees(ctx, WorktreeQuery{Task: "T"})
	if err != nil || total != 2 {
		t.Fatalf("%v %d", err, total)
	}
	for _, w := range got {
		if w.Alive != (w.Path == live) {
			t.Fatalf("%+v", w)
		}
	}
}
