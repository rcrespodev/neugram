package store

import (
	"context"
	"testing"
	"time"
)

func TestSetTaskStatusReportsPreviousAndTouchesUpdatedAt(t *testing.T) {
	s, c := newTestStore(t)
	mustCreateTask(t, s, NewTask{Key: "E", Title: "epico"})
	mustCreateTask(t, s, NewTask{Key: "E-1", Title: "hija", Parent: "E"})
	c.advance(time.Hour)

	got, err := s.SetTaskStatus(context.Background(), "e-1", "in-progress")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "in-progress" || got.PreviousStatus != "todo" || got.TaskKey != "E-1" ||
		*got.ParentKey != "E" || got.OpenChildren != 0 || got.UpdatedAt != c.now().Format(TimeLayout) {
		t.Fatalf("%+v", got)
	}
}

func TestSetTaskStatusDoneWithOpenChildrenSucceedsAndReportsThem(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreateTask(t, s, NewTask{Key: "E", Title: "epico"})
	mustCreateTask(t, s, NewTask{Key: "E-1", Title: "a", Parent: "E"})
	mustCreateTask(t, s, NewTask{Key: "E-2", Title: "b", Parent: "E"})
	mustCreateTask(t, s, NewTask{Key: "E-1-a", Title: "nieta", Parent: "E-1"})
	if _, err := s.SetTaskStatus(context.Background(), "E-2", "done"); err != nil {
		t.Fatal(err)
	}

	got, err := s.SetTaskStatus(context.Background(), "E", "done")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" || got.OpenChildren != 1 {
		t.Fatalf("%+v", got)
	}
	if st := queryValue[string](t, s.db, `SELECT status FROM tasks WHERE key = 'E'`); st != "done" {
		t.Fatalf("status = %s", st)
	}
}

func TestSetTaskStatusRejectsBadInput(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreateTask(t, s, NewTask{Key: "E", Title: "epico"})
	_, err := s.SetTaskStatus(context.Background(), "E", "listo")
	wantCode(t, err, ErrInvalidEnum)
	_, err = s.SetTaskStatus(context.Background(), "nada", "done")
	wantCode(t, err, ErrUnknownTask)
	_, err = s.SetTaskStatus(context.Background(), "none", "done")
	wantCode(t, err, ErrUnknownTask)
}
