// Package state implements the task lifecycle finite state machine used by the
// rlm-agent orchestrator.
//
// The happy path is
//
//	Request -> Planning -> DevQA (loop) -> Review -> Success
//
// A CriticalPause event suspends the machine from any active state; Resume
// restores whichever active state was current when the pause happened. Success
// is terminal: no event leaves it.
package state

import (
	"errors"
	"fmt"
)

// State is a node in the task lifecycle graph.
type State string

const (
	// Request is the initial state: a task was accepted but not started.
	Request State = "Request"
	// Planning means the planner agent is decomposing the task into plan/.
	Planning State = "Planning"
	// DevQA means the developer and tester agents are iterating.
	DevQA State = "DevQA"
	// Review means the result is being validated against the plan.
	Review State = "Review"
	// Success is the terminal state: the task is done.
	Success State = "Success"
	// Pause means the machine is suspended, waiting for a human answer.
	Pause State = "Pause"
)

// Event is an input that drives the state machine.
type Event string

const (
	// Start moves a fresh request into planning.
	Start Event = "Start"
	// PlanDone means the planner produced plan/ (todo.md, architecture.md).
	PlanDone Event = "PlanDone"
	// DevQALoop is one more developer/tester iteration inside DevQA.
	DevQALoop Event = "DevQALoop"
	// ReviewStart means the dev/QA loop converged, so review begins.
	ReviewStart Event = "ReviewStart"
	// ReviewOK means review passed and the task is done.
	ReviewOK Event = "ReviewOK"
	// CriticalPause suspends the task for human-in-the-loop input.
	CriticalPause Event = "CriticalPause"
	// Resume continues a paused task from its previous active state.
	Resume Event = "Resume"
	// Fail abandons the current attempt and sends the task back to planning.
	// The machine stays dumb here: the retry budget decides whether another
	// attempt is allowed at all.
	Fail Event = "Fail"
)

var (
	// ErrInvalidTransition reports an event that is not allowed in the current state.
	ErrInvalidTransition = errors.New("invalid state transition")
	// ErrUnknownState reports a state value outside the defined constants.
	ErrUnknownState = errors.New("unknown state")
	// ErrUnknownEvent reports an event value outside the defined constants.
	ErrUnknownEvent = errors.New("unknown event")
)

// Valid reports whether s is one of the defined states.
func (s State) Valid() bool {
	switch s {
	case Request, Planning, DevQA, Review, Success, Pause:
		return true
	default:
		return false
	}
}

// Active reports whether s is a working state, i.e. one that can be interrupted
// by CriticalPause and restored by Resume.
func (s State) Active() bool {
	switch s {
	case Request, Planning, DevQA, Review:
		return true
	default:
		return false
	}
}

// Terminal reports whether s ends the task lifecycle.
func (s State) Terminal() bool {
	return s == Success
}

// Valid reports whether e is one of the defined events.
func (e Event) Valid() bool {
	switch e {
	case Start, PlanDone, DevQALoop, ReviewStart, ReviewOK, CriticalPause, Resume, Fail:
		return true
	default:
		return false
	}
}

// transitions is the static edge table for events that do not depend on
// history. CriticalPause and Resume are handled separately in Transition
// because they depend on the previous state.
var transitions = map[State]map[Event]State{
	Request: {
		Start: Planning,
	},
	Planning: {
		PlanDone: DevQA,
	},
	DevQA: {
		DevQALoop:   DevQA,
		ReviewStart: Review,
		Fail:        Planning,
	},
	Review: {
		ReviewOK: Success,
		Fail:     Planning,
	},
}

// Transition computes the next state for event in state from.
//
// previous is the active state recorded when the task was paused; it is only
// consulted for Resume. On any error the returned state is from unchanged, so
// callers may keep using the machine after a rejected event.
func Transition(from, previous State, event Event) (State, error) {
	if !from.Valid() {
		return from, fmt.Errorf("%w: %q", ErrUnknownState, from)
	}
	if !event.Valid() {
		return from, fmt.Errorf("%w: %q", ErrUnknownEvent, event)
	}

	switch event {
	case CriticalPause:
		if !from.Active() {
			return from, fmt.Errorf("%w: %s from %s", ErrInvalidTransition, event, from)
		}
		return Pause, nil
	case Resume:
		if from != Pause {
			return from, fmt.Errorf("%w: %s from %s", ErrInvalidTransition, event, from)
		}
		if !previous.Active() {
			return from, fmt.Errorf("%w: %s without a recorded active state (previous=%q)",
				ErrInvalidTransition, event, previous)
		}
		return previous, nil
	}

	next, ok := transitions[from][event]
	if !ok {
		return from, fmt.Errorf("%w: %s from %s", ErrInvalidTransition, event, from)
	}
	return next, nil
}

// Machine is a stateful wrapper around Transition. The zero value is not ready
// to use; call New.
type Machine struct {
	state    State
	previous State
}

// New returns a machine in the initial Request state.
func New() *Machine {
	return &Machine{state: Request}
}

// State reports the current state.
func (m *Machine) State() State {
	return m.state
}

// Previous reports the active state recorded at the last CriticalPause, or the
// empty string when the task is not paused.
func (m *Machine) Previous() State {
	return m.previous
}

// Apply drives the machine with event and returns the resulting state.
// On error the machine is left untouched.
func (m *Machine) Apply(event Event) (State, error) {
	next, err := Transition(m.state, m.previous, event)
	if err != nil {
		return m.state, err
	}

	switch {
	case next == Pause:
		// Remember where to resume from.
		m.previous = m.state
	case m.state == Pause:
		// Resumed: the bookmark is consumed.
		m.previous = ""
	}

	m.state = next
	return m.state, nil
}
