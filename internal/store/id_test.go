package store

import (
	"testing"
	"time"
)

func TestNewIDIsAULIDThatSortsByTime(t *testing.T) {
	a := NewID(time.UnixMilli(1469918176385))
	b := NewID(time.UnixMilli(1469918176386))
	if len(a) != 26 || a >= b {
		t.Fatalf("a=%q b=%q", a, b)
	}
}
