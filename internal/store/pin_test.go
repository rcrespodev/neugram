package store

import (
	"context"
	"errors"
	"testing"
)

func addProduct(t *testing.T, s *Store, name string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO products (id, name, created_at, updated_at) VALUES (?, ?, 'x', 'x')`,
		NewID(s.now()), name); err != nil {
		t.Fatal(err)
	}
}

func mustCreateTask(t *testing.T, s *Store, in NewTask) TaskCard {
	t.Helper()
	c, err := s.CreateTask(context.Background(), in)
	if err != nil {
		t.Fatalf("%+v: %v", in, err)
	}
	return c
}

func wantCode(t *testing.T, err error, want *Error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want code %s", err, want.Code)
	}
}

func TestCreateTaskDefaultsToTodoAndRejectsDuplicates(t *testing.T) {
	s, _ := newTestStore(t)
	c := mustCreateTask(t, s, NewTask{Key: "T-1", Title: "uno", Notes: "n", Tracker: &Tracker{Kind: "clickup", ID: "9", URL: "u"}})
	if c.Status != "todo" || c.ParentKey != nil || c.Tracker == nil || c.Tracker.ID != "9" || *c.Notes != "n" {
		t.Fatalf("%+v", c)
	}
	_, err := s.CreateTask(context.Background(), NewTask{Key: "t-1", Title: "otra"})
	wantCode(t, err, ErrTaskExists)
	_, err = s.CreateTask(context.Background(), NewTask{Key: "none", Title: "x"})
	wantCode(t, err, ErrInvalidRequest)
	_, err = s.CreateTask(context.Background(), NewTask{Key: "T-2"})
	wantCode(t, err, ErrInvalidRequest)
}

func TestCreateTaskCopiesProductFromParent(t *testing.T) {
	s, _ := newTestStore(t)
	addProduct(t, s, "Microservices")
	addProduct(t, s, "Otro")
	mustCreateTask(t, s, NewTask{Key: "E", Title: "epico", Product: "microservices"})
	child := mustCreateTask(t, s, NewTask{Key: "E-1", Title: "hija", Parent: "E"})
	if child.Product == nil || *child.Product != "Microservices" || *child.ParentKey != "E" {
		t.Fatalf("%+v", child)
	}
	own := mustCreateTask(t, s, NewTask{Key: "E-2", Title: "hija", Parent: "E", Product: "Otro"})
	if *own.Product != "Otro" {
		t.Fatalf("%+v", own)
	}
	_, err := s.CreateTask(context.Background(), NewTask{Key: "X", Title: "x", Product: "Nada"})
	wantCode(t, err, ErrUnknownProduct)
}

func TestCreateTaskValidatesHierarchy(t *testing.T) {
	s, _ := newTestStore(t)
	mustCreateTask(t, s, NewTask{Key: "L1", Title: "1"})
	mustCreateTask(t, s, NewTask{Key: "L2", Title: "2", Parent: "L1"})
	l3 := mustCreateTask(t, s, NewTask{Key: "L3", Title: "3", Parent: "L2"})
	if len(l3.AncestorKeys) != 2 {
		t.Fatalf("%+v", l3)
	}
	_, err := s.CreateTask(context.Background(), NewTask{Key: "L4", Title: "4", Parent: "L3"})
	wantCode(t, err, ErrInvalidParent)
	_, err = s.CreateTask(context.Background(), NewTask{Key: "L5", Title: "5", Parent: "no-existe"})
	wantCode(t, err, ErrInvalidParent)
	if n := queryValue[int](t, s.db, `SELECT count(*) FROM tasks`); n != 3 {
		t.Fatalf("tasks = %d", n)
	}
}

func TestPinSessionNoneLeavesTaskNullAndStampsPinnedAt(t *testing.T) {
	s, c := newTestStore(t)
	res, err := s.PinSession(context.Background(), PinRequest{SessionID: "sess-1", Target: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if res.TaskID != nil || res.TaskKey != "none" || res.Created || res.PinnedBy != "agent" || res.PinnedAt != c.now().Format(TimeLayout) {
		t.Fatalf("%+v", res)
	}
	sess, _ := s.GetSession(context.Background(), "sess-1")
	if sess.TaskID != "" || sess.PinnedAt == "" || sess.PinnedBy != "agent" {
		t.Fatalf("%+v", sess)
	}
}

func TestPinSessionRepinReportsPrevious(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateTask(t, s, NewTask{Key: "A", Title: "a"})
	b := mustCreateTask(t, s, NewTask{Key: "B", Title: "b"})
	first, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "a"})
	if err != nil || first.RepinnedFrom != nil || first.TaskKey != "A" {
		t.Fatalf("%+v %v", first, err)
	}
	again, _ := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "A"})
	if again.RepinnedFrom != nil {
		t.Fatalf("same pin: %+v", again)
	}
	second, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: b.TaskID, PinnedBy: "cli"})
	if err != nil || second.RepinnedFrom == nil || *second.RepinnedFrom != "A" || second.PinnedBy != "cli" {
		t.Fatalf("%+v %v", second, err)
	}
	third, _ := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "none"})
	if *third.RepinnedFrom != "B" {
		t.Fatalf("%+v", third)
	}
	fourth, _ := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "A"})
	if *fourth.RepinnedFrom != "none" {
		t.Fatalf("%+v", fourth)
	}
}

func TestPinSessionCreatesTaskOnlyWithTitle(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	addProduct(t, s, "Microservices")
	mustCreateTask(t, s, NewTask{Key: "E", Title: "epico", Product: "Microservices"})

	_, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "E-1"})
	wantCode(t, err, ErrUnknownTask)
	if sess, _ := s.GetSession(ctx, "sess-1"); sess.PinnedAt != "" {
		t.Fatalf("failed pin left a pin: %+v", sess)
	}

	res, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "E-1", Title: "hija", Parent: "E"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Status != "todo" || *res.Product != "Microservices" || *res.ParentKey != "E" ||
		res.TaskID == nil || len(res.AncestorKeys) != 1 {
		t.Fatalf("%+v", res)
	}

	existing, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "E", Title: "ignorado"})
	if err != nil || existing.Created || existing.Title != "epico" {
		t.Fatalf("%+v %v", existing, err)
	}
}

func TestPinSessionRollsBackTheTaskWhenCreationFails(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.PinSession(context.Background(), PinRequest{SessionID: "sess-1", Target: "N", Title: "n", Product: "Nada"})
	wantCode(t, err, ErrUnknownProduct)
	if n := queryValue[int](t, s.db, `SELECT count(*) FROM tasks`); n != 0 {
		t.Fatalf("tasks = %d", n)
	}
}

func TestPinSessionRejectsBadInput(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.PinSession(ctx, PinRequest{SessionID: "ghost", Target: "none"})
	wantCode(t, err, ErrUnknownSession)
	_, err = s.PinSession(ctx, PinRequest{SessionID: "sess-1"})
	wantCode(t, err, ErrInvalidRequest)
	_, err = s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "none", PinnedBy: "alguien"})
	wantCode(t, err, ErrInvalidRequest)
}
