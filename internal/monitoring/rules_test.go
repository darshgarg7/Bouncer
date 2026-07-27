package monitoring

import "testing"

func TestTrackerDetectsNoProgressAndAlternation(t *testing.T) {
	tracker, err := New(Config{RejectionThreshold: 2, NoProgressThreshold: 3, AlternationThreshold: 4})
	if err != nil {
		t.Fatal(err)
	}
	operations := []string{"filesystem.read", "state.validate", "filesystem.read", "state.validate"}
	var window Window
	for _, operation := range operations {
		window, err = tracker.Observe(Observation{
			RejectedCandidates: 2,
			CandidateCount:     3,
			Operation:          operation,
			LatencyMS:          10,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{
		AlertRejectionBurst:  true,
		AlertNoProgressLoop:  true,
		AlertToolAlternation: true,
	}
	for _, alert := range window.RuleAlerts {
		delete(want, alert)
	}
	if len(want) != 0 {
		t.Fatalf("missing alerts %v from %+v", want, window)
	}
}

func TestTrackerResetsNoProgressOnMeasuredProgress(t *testing.T) {
	tracker, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.Observe(Observation{CandidateCount: 1}); err != nil {
		t.Fatal(err)
	}
	window, err := tracker.Observe(Observation{CandidateCount: 1, ProgressDelta: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if window.Features.NoProgressStreak != 0 {
		t.Fatalf("no-progress streak did not reset: %+v", window)
	}
}
