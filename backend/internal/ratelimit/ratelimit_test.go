package ratelimit

import (
	"testing"
	"time"

	"teyljunior/internal/core"
)

func TestFixedWindow(t *testing.T) {
	clock := core.NewFixedClock(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC))
	l := New(clock, 2, time.Minute)
	for range 2 {
		if ok, _ := l.Allow("u"); !ok {
			t.Fatal("within limit rejected")
		}
	}
	ok, retry := l.Allow("u")
	if ok || retry != time.Minute {
		t.Fatalf("over limit: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys are not independent")
	}
	clock.Advance(time.Minute)
	if ok, _ := l.Allow("u"); !ok {
		t.Fatal("window did not reset")
	}
	if ok, _ := New(clock, 0, time.Minute).Allow("u"); !ok {
		t.Fatal("zero limit must disable limiting")
	}
}
