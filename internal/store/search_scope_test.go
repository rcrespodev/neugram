package store

import (
	"context"
	"strings"
	"testing"
)

func scopeFixture(t *testing.T) *Store {
	t.Helper()
	s, _ := newTestStore(t)
	addProduct(t, s, "Prod")
	mustCreateTask(t, s, NewTask{Key: "EPIC", Title: "epico", Product: "Prod"})
	mustCreateTask(t, s, NewTask{Key: "TASK", Title: "tarea", Parent: "EPIC"})
	mustCreateTask(t, s, NewTask{Key: "SUB", Title: "sub", Parent: "TASK"})
	mustCreateTask(t, s, NewTask{Key: "OTHER", Title: "otra"})
	for _, k := range []string{"EPIC", "TASK", "SUB", "OTHER"} {
		save(t, s, NewObservation{Title: "obs " + k, Content: "alfa de " + k, TaskID: k})
	}
	return s
}

func titles(rs []SearchResult) map[string]bool {
	m := map[string]bool{}
	for _, r := range rs {
		m[r.Title] = true
	}
	return m
}

func TestSearchByTaskIncludesAncestorsByDefault(t *testing.T) {
	s := scopeFixture(t)
	got, _ := search(t, s, SearchParams{Query: "alfa", Task: "SUB"})
	m := titles(got)
	if len(m) != 3 || !m["obs SUB"] || !m["obs TASK"] || !m["obs EPIC"] {
		t.Fatalf("%v", m)
	}
	got, _ = search(t, s, SearchParams{Query: "alfa", Task: "SUB", SkipAncestors: true})
	if m := titles(got); len(m) != 1 || !m["obs SUB"] {
		t.Fatalf("%v", m)
	}
}

func TestSearchByTaskIncludeSubtasksGoesDownTheWholeTree(t *testing.T) {
	s := scopeFixture(t)
	got, _ := search(t, s, SearchParams{Query: "alfa", Task: "EPIC", IncludeSubtasks: true})
	if m := titles(got); len(m) != 3 || m["obs OTHER"] {
		t.Fatalf("%v", m)
	}
	got, _ = search(t, s, SearchParams{Query: "alfa", Task: "EPIC"})
	if m := titles(got); len(m) != 1 || !m["obs EPIC"] {
		t.Fatalf("%v", m)
	}
}

func TestSearchByUnknownTask(t *testing.T) {
	s := scopeFixture(t)
	_, _, err := s.SearchObservations(context.Background(), SearchParams{Task: "NOPE"})
	wantCode(t, err, ErrUnknownTask)
}

func TestSearchByProduct(t *testing.T) {
	s := scopeFixture(t)
	got, _ := search(t, s, SearchParams{Query: "alfa", Product: "prod"})
	if m := titles(got); len(m) != 3 || m["obs OTHER"] {
		t.Fatalf("%v", m)
	}
}

func TestSearchWhereFixedColumnsAndInvalidField(t *testing.T) {
	s := scopeFixture(t)
	got, _ := search(t, s, SearchParams{Where: map[string]any{"type": "decision", "topic_key": nil, "pinned": 0}})
	if len(got) != 4 {
		t.Fatalf("%d", len(got))
	}
	got, _ = search(t, s, SearchParams{Where: map[string]any{"title": "obs SUB"}})
	if len(got) != 1 {
		t.Fatalf("%d", len(got))
	}
	_, _, err := s.SearchObservations(context.Background(), SearchParams{Where: map[string]any{"impacto": "high"}})
	wantCode(t, err, ErrInvalidField)
	_, _, err = s.SearchObservations(context.Background(), SearchParams{Where: map[string]any{"type; DROP": 1}})
	wantCode(t, err, ErrInvalidField)
}

func TestSearchResultPreviewIsTruncated(t *testing.T) {
	s, _ := newTestStore(t)
	save(t, s, NewObservation{Title: "largo", Content: strings.Repeat("x", 400)})
	got, _ := search(t, s, SearchParams{})
	if len(got[0].Preview) != previewLen || len(got[0].Content) <= previewLen {
		t.Fatalf("%d %d", len(got[0].Preview), len(got[0].Content))
	}
}
