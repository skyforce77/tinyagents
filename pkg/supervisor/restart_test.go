package supervisor

import (
	"testing"
	"time"
)

func TestRestartBackoff(t *testing.T) {
	t.Parallel()
	// maxRestarts=0 means "always restart" — isolates the backoff curve.
	s := NewRestart(0, time.Minute, 10*time.Millisecond, 100*time.Millisecond)
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 10 * time.Millisecond},
		{2, 20 * time.Millisecond},
		{3, 40 * time.Millisecond},
		{4, 80 * time.Millisecond},
		{5, 100 * time.Millisecond}, // capped
		{10, 100 * time.Millisecond},
	}
	for _, tc := range tests {
		d := s.Decide(nil, tc.attempt, time.Now())
		if d.Directive != Restart {
			t.Errorf("attempt %d: directive %v, want Restart", tc.attempt, d.Directive)
		}
		if d.Delay != tc.want {
			t.Errorf("attempt %d: delay %v, want %v", tc.attempt, d.Delay, tc.want)
		}
	}
}

func TestRestartMaxWithinWindow(t *testing.T) {
	t.Parallel()
	s := NewRestart(3, time.Minute, 10*time.Millisecond, 10*time.Millisecond)
	d := s.Decide(nil, 4, time.Now())
	if d.Directive != Stop {
		t.Fatalf("over maxRestarts: directive %v, want Stop", d.Directive)
	}
}

func TestRestartOutsideWindowStillRestarts(t *testing.T) {
	t.Parallel()
	s := NewRestart(3, 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond)
	oldStart := time.Now().Add(-time.Hour)
	d := s.Decide(nil, 10, oldStart)
	if d.Directive != Restart {
		t.Fatalf("failure count older than window: directive %v, want Restart", d.Directive)
	}
}

func TestStopStrategy(t *testing.T) {
	t.Parallel()
	s := NewStop()
	if s.Decide(nil, 1, time.Now()).Directive != Stop {
		t.Fatal("Stop strategy should always Stop")
	}
}
