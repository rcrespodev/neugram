package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestUpsertWorktreeCreatesThenUpdatesByPath(t *testing.T) {
	s, c := newTestStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, NewTask{Key: "T", Title: "t"})
	dir := t.TempDir()

	first, err := s.UpsertWorktree(ctx, NewWorktree{Path: dir, Repo: "neugram", Branch: "feat/a", Base: "main",
		Purpose: "uno", Task: "T", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.Alive || *first.TaskKey != "T" || *first.Base != "main" ||
		*first.CreatedBySession != "sess-1" || first.ID == "" {
		t.Fatalf("%+v", first)
	}

	c.advance(time.Hour)
	second, err := s.UpsertWorktree(ctx, NewWorktree{Path: dir, Repo: "neugram", Branch: "feat/b", SessionID: "sess-2"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.ID != first.ID || second.Branch != "feat/b" || second.Base != nil ||
		*second.TaskKey != "T" || *second.CreatedBySession != "sess-1" || second.CreatedAt != first.CreatedAt {
		t.Fatalf("%+v", second)
	}
	if n := queryValue[int](t, s.db, `SELECT count(*) FROM worktrees`); n != 1 {
		t.Fatalf("worktrees = %d", n)
	}
}

func TestUpsertWorktreeComputesAliveFromTheFilesystem(t *testing.T) {
	s, _ := newTestStore(t)
	gone := t.TempDir() + "/gone"
	w, err := s.UpsertWorktree(context.Background(), NewWorktree{Path: gone, Repo: "r", Branch: "b"})
	if err != nil || w.Alive || w.TaskKey != nil {
		t.Fatalf("%+v %v", w, err)
	}
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	w, _ = s.UpsertWorktree(context.Background(), NewWorktree{Path: gone, Repo: "r", Branch: "b"})
	if !w.Alive {
		t.Fatalf("%+v", w)
	}
}

func TestUpsertWorktreeRejectsBadInput(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	for _, in := range []NewWorktree{
		{Path: "relativo/wt", Repo: "r", Branch: "b"},
		{Path: "", Repo: "r", Branch: "b"},
		{Path: "/abs/wt", Branch: "b"},
		{Path: "/abs/wt", Repo: "r"},
	} {
		_, err := s.UpsertWorktree(ctx, in)
		wantCode(t, err, ErrInvalidRequest)
	}
	_, err := s.UpsertWorktree(ctx, NewWorktree{Path: "/abs/wt", Repo: "r", Branch: "b", Task: "nada"})
	wantCode(t, err, ErrUnknownTask)
	if n := queryValue[int](t, s.db, `SELECT count(*) FROM worktrees`); n != 0 {
		t.Fatalf("worktrees = %d", n)
	}
}
