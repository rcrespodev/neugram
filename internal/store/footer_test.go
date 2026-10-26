package store

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateKeepsUnresolvableFooterWhenTaskUnchanged(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res := save(t, s, NewObservation{Title: "t", Content: "viejo\n\n**Task**: LEGACY-1 — importada"})
	content := "nuevo\n\n**Task**: LEGACY-1 — importada"
	up, err := s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{Content: &content})
	if err != nil || up.Observation.Content != content || up.FooterRewritten {
		t.Fatalf("%+v %v", up, err)
	}
	mustCreateTask(t, s, NewTask{Key: "T-2", Title: "Otra tarea"})
	task := "T-2"
	up, err = s.UpdateObservation(ctx, res.Observation.SyncID, ObservationPatch{TaskID: &task})
	if err != nil || up.Observation.Content != "nuevo\n\n**Task**: T-2 — Otra tarea" || !up.FooterRewritten {
		t.Fatalf("%+v %v", up, err)
	}
}

func TestFooterShapes(t *testing.T) {
	cases := []struct {
		name, content, want string
		rewritten           bool
	}{
		{"crlf", "a\r\n\r\n**Task**: T-1 — Una tarea\r\n", "a\r\n\r\n**Task**: T-1 — Una tarea\r\n", false},
		{"espacios", "a\n\n  **Task**:   T-1 — Una tarea  ", "a\n\n  **Task**:   T-1 — Una tarea  ", false},
		{"cita en medio", "a **Task**: X-1 — cita\nb", "a **Task**: X-1 — cita\nb\n\n**Task**: T-1 — Una tarea", false},
		{"cita al final", "a\n**Task**: X-1 — cita", "a\n\n**Task**: T-1 — Una tarea", true},
		{"multiples pies", "a\n\n**Task**: X-1 — uno\n\n**Task**: T-1 — Una tarea", "a\n\n**Task**: X-1 — uno\n\n**Task**: T-1 — Una tarea", false},
		{"ninguna con sufijo", "a\n\n**Task**: ningunax", "a\n\n**Task**: T-1 — Una tarea", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rewritten := withTaskFooter(c.content, "T-1", "Una tarea")
			if got != c.want || rewritten != c.rewritten {
				t.Fatalf("got %q %v", got, rewritten)
			}
		})
	}
}

func TestHandoffWithChangedPinOverwritesAndRetargets(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	pinned(t, s, "T-1", "Una tarea")
	first, err := s.SaveHandoff(ctx, Handoff{SessionID: "sess-1", Content: "uno"})
	if err != nil {
		t.Fatal(err)
	}
	mustCreateTask(t, s, NewTask{Key: "T-2", Title: "Otra tarea"})
	if _, err := s.PinSession(ctx, PinRequest{SessionID: "sess-1", Target: "T-2"}); err != nil {
		t.Fatal(err)
	}
	second, err := s.SaveHandoff(ctx, Handoff{SessionID: "sess-1", Content: "dos"})
	if err != nil || !second.Replaced || second.Observation.SyncID != first.Observation.SyncID ||
		second.TaskKey != "T-2" || !strings.HasSuffix(second.Observation.Content, "**Task**: T-2 — Otra tarea") {
		t.Fatalf("%+v %v", second, err)
	}
}

func TestTopicKeyUpsertWithForeignFooter(t *testing.T) {
	s, _ := newTestStore(t)
	pinned(t, s, "T-1", "Una tarea")
	first, _ := add(s, NewObservation{Content: "uno", TopicKey: "k"})
	second, err := add(s, NewObservation{Content: "dos\n\n**Task**: X-9 — ajena", TopicKey: "k"})
	if err != nil || !second.Upserted || second.Observation.SyncID != first.Observation.SyncID || !second.FooterRewritten ||
		second.Observation.Content != "dos\n\n**Task**: T-1 — Una tarea" ||
		second.Observation.NormalizedHash != NormalizedHash(second.Observation.Content) {
		t.Fatalf("%+v %v", second, err)
	}
}
