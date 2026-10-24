package state

import (
	"errors"
	"testing"
)

// allEvents is used to prove that Success rejects every possible input.
var allEvents = []Event{
	Start, PlanDone, DevQALoop, ReviewStart, ReviewOK, CriticalPause, Resume, Fail,
}

func TestTransitionValid(t *testing.T) {
	tests := []struct {
		name     string
		from     State
		previous State
		event    Event
		want     State
	}{
		{"request starts", Request, "", Start, Planning},
		{"planner hands off", Planning, "", PlanDone, DevQA},
		{"devqa iterates", DevQA, "", DevQALoop, DevQA},
		{"devqa converges", DevQA, "", ReviewStart, Review},
		{"review passes", Review, "", ReviewOK, Success},
		{"devqa failure re-plans", DevQA, "", Fail, Planning},
		{"review failure re-plans", Review, "", Fail, Planning},
		{"pause from request", Request, "", CriticalPause, Pause},
		{"pause from planning", Planning, "", CriticalPause, Pause},
		{"pause from devqa", DevQA, "", CriticalPause, Pause},
		{"pause from review", Review, "", CriticalPause, Pause},
		{"resume to request", Pause, Request, Resume, Request},
		{"resume to planning", Pause, Planning, Resume, Planning},
		{"resume to devqa", Pause, DevQA, Resume, DevQA},
		{"resume to review", Pause, Review, Resume, Review},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Transition(tc.from, tc.previous, tc.event)
			if err != nil {
				t.Fatalf("Transition(%s, %s, %s) returned unexpected error: %v",
					tc.from, tc.previous, tc.event, err)
			}
			if got != tc.want {
				t.Fatalf("Transition(%s, %s, %s) = %s, want %s",
					tc.from, tc.previous, tc.event, got, tc.want)
			}
		})
	}
}

func TestTransitionInvalid(t *testing.T) {
	tests := []struct {
		name     string
		from     State
		previous State
		event    Event
		wantErr  error
	}{
		{"no start twice", Planning, "", Start, ErrInvalidTransition},
		{"no plan done from request", Request, "", PlanDone, ErrInvalidTransition},
		{"no review ok from devqa", DevQA, "", ReviewOK, ErrInvalidTransition},
		{"no review start from planning", Planning, "", ReviewStart, ErrInvalidTransition},
		{"no fail from request", Request, "", Fail, ErrInvalidTransition},
		{"resume outside pause", Request, Planning, Resume, ErrInvalidTransition},
		{"pause from success", Success, "", CriticalPause, ErrInvalidTransition},
		{"pause from pause", Pause, Review, CriticalPause, ErrInvalidTransition},
		{"resume without bookmark", Pause, "", Resume, ErrInvalidTransition},
		{"resume onto terminal state", Pause, Success, Resume, ErrInvalidTransition},
		{"unknown state", State("Bogus"), "", Start, ErrUnknownState},
		{"empty state", State(""), "", Start, ErrUnknownState},
		{"unknown event", Request, "", Event("Explode"), ErrUnknownEvent},
		{"empty event", Request, "", Event(""), ErrUnknownEvent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Transition(tc.from, tc.previous, tc.event)
			if err == nil {
				t.Fatalf("Transition(%s, %s, %s) = %s, want error",
					tc.from, tc.previous, tc.event, got)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Transition(%s, %s, %s) error = %v, want %v",
					tc.from, tc.previous, tc.event, err, tc.wantErr)
			}
			if got != tc.from {
				t.Fatalf("Transition(%s, %s, %s) returned state %s on error, want unchanged %s",
					tc.from, tc.previous, tc.event, got, tc.from)
			}
		})
	}
}

// TestMachinePauseRoundTrip walks Request -> Planning -> DevQA -> Review, pauses
// from each active state in turn and checks that Resume restores it.
func TestMachinePauseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		path []Event
	}{
		{"from request", nil},
		{"from planning", []Event{Start}},
		{"from devqa", []Event{Start, PlanDone}},
		{"from review", []Event{Start, PlanDone, ReviewStart}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			for _, ev := range tc.path {
				if _, err := m.Apply(ev); err != nil {
					t.Fatalf("Apply(%s) failed: %v", ev, err)
				}
			}

			active := m.State()
			if !active.Active() {
				t.Fatalf("expected an active state before pause, got %s", active)
			}
			if m.Previous() != "" {
				t.Fatalf("Previous() = %q before any pause, want empty", m.Previous())
			}

			got, err := m.Apply(CriticalPause)
			if err != nil {
				t.Fatalf("Apply(CriticalPause) from %s failed: %v", active, err)
			}
			if got != Pause || m.State() != Pause {
				t.Fatalf("state after CriticalPause = %s, want %s", got, Pause)
			}
			if m.Previous() != active {
				t.Fatalf("Previous() after pause = %s, want %s", m.Previous(), active)
			}

			got, err = m.Apply(Resume)
			if err != nil {
				t.Fatalf("Apply(Resume) failed: %v", err)
			}
			if got != active {
				t.Fatalf("state after Resume = %s, want %s", got, active)
			}
			if m.Previous() != "" {
				t.Fatalf("Previous() after resume = %q, want empty", m.Previous())
			}
		})
	}
}

func TestMachineFullJourney(t *testing.T) {
	m := New()

	steps := []struct {
		event Event
		want  State
	}{
		{Start, Planning},
		{PlanDone, DevQA},
		{DevQALoop, DevQA},
		{DevQALoop, DevQA},
		{ReviewStart, Review},
		{CriticalPause, Pause},
		{Resume, Review},
		{ReviewOK, Success},
	}

	for i, step := range steps {
		got, err := m.Apply(step.event)
		if err != nil {
			t.Fatalf("step %d: Apply(%s) from %s failed: %v", i, step.event, m.State(), err)
		}
		if got != step.want {
			t.Fatalf("step %d: Apply(%s) = %s, want %s", i, step.event, got, step.want)
		}
	}
}

// TestMachineInvalidKeepsState guards the "rejected event is a no-op" contract.
func TestMachineInvalidKeepsState(t *testing.T) {
	m := New()
	if _, err := m.Apply(Start); err != nil {
		t.Fatalf("Apply(Start) failed: %v", err)
	}

	if _, err := m.Apply(ReviewOK); err == nil {
		t.Fatal("Apply(ReviewOK) from Planning returned no error")
	}
	if m.State() != Planning {
		t.Fatalf("state after rejected event = %s, want %s", m.State(), Planning)
	}

	// A rejected event must not poison the machine: the real next event works.
	if _, err := m.Apply(PlanDone); err != nil {
		t.Fatalf("machine unusable after rejected event: %v", err)
	}
	if m.State() != DevQA {
		t.Fatalf("state after PlanDone = %s, want %s", m.State(), DevQA)
	}
}

// TestSuccessTerminal proves no event leaves Success.
func TestSuccessTerminal(t *testing.T) {
	if !Success.Terminal() {
		t.Fatal("Success.Terminal() = false, want true")
	}
	if Success.Active() {
		t.Fatal("Success.Active() = true, want false")
	}

	m := New()
	for _, ev := range []Event{Start, PlanDone, ReviewStart, ReviewOK} {
		if _, err := m.Apply(ev); err != nil {
			t.Fatalf("setup: Apply(%s) failed: %v", ev, err)
		}
	}
	if m.State() != Success {
		t.Fatalf("setup left state %s, want %s", m.State(), Success)
	}

	for _, ev := range allEvents {
		got, err := m.Apply(ev)
		if err == nil {
			t.Fatalf("Apply(%s) from Success = %s, want error", ev, got)
		}
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("Apply(%s) from Success error = %v, want %v", ev, err, ErrInvalidTransition)
		}
		if m.State() != Success {
			t.Fatalf("state left Success after %s: %s", ev, m.State())
		}
	}
}

func TestStatePredicates(t *testing.T) {
	active := map[State]bool{Request: true, Planning: true, DevQA: true, Review: true}
	for _, s := range []State{Request, Planning, DevQA, Review, Success, Pause} {
		if !s.Valid() {
			t.Errorf("%s.Valid() = false, want true", s)
		}
		if got := s.Active(); got != active[s] {
			t.Errorf("%s.Active() = %v, want %v", s, got, active[s])
		}
		if got := s.Terminal(); got != (s == Success) {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, s == Success)
		}
	}
	if State("Bogus").Valid() {
		t.Error("State(\"Bogus\").Valid() = true, want false")
	}
	if Event("Bogus").Valid() {
		t.Error("Event(\"Bogus\").Valid() = true, want false")
	}
}
