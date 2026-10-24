// Package orchestrator drives one rlm-agent task through the lifecycle state
// machine, calling LLM agents and applying their files through narrow
// interfaces.
//
// Everything the core talks to is an interface (Agent, Executor), so the whole
// task lifecycle can be exercised with fakes: no network, no container, no
// model. The orchestrator itself owns only policy - which agent runs in which
// state, when a pause is required, and when the retry budget is spent.
package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"rlm-agent/internal/budget"
	"rlm-agent/internal/log"
	"rlm-agent/internal/response"
	"rlm-agent/internal/state"
)

// Statuses an agent reports in response.Response.Status. The orchestrator
// compares them as exact strings; anything else is treated as "not done yet"
// rather than guessed at.
const (
	// StatusPlanningDone means the planner finished plan/ (todo.md, architecture.md).
	StatusPlanningDone = "planning_done"
	// StatusCodeDone means the developer wrote or changed code.
	StatusCodeDone = "code_done"
	// StatusPass means the tester ran the suite and everything passed.
	StatusPass = "pass"
	// StatusFail means the tester found a failure worth another round.
	StatusFail = "fail"
	// StatusBlocked means the agent cannot proceed without human input.
	StatusBlocked = "blocked"
	// StatusNeedsPause means the agent explicitly asks to suspend the task.
	StatusNeedsPause = "needs_pause"
)

// ErrNilResponse reports an Agent that returned neither a response nor an
// error. Treating it as an empty answer would silently skip work.
var ErrNilResponse = errors.New("orchestrator: agent returned nil response")

// Agent is one LLM turn. role is the agent's name ("planner", "developer",
// "tester"); prompt is the task or the review request.
type Agent interface {
	Run(ctx context.Context, role, prompt string) (*response.Response, error)
}

// Executor applies what an agent produced to the working tree / sandbox.
// RunCommands executes shell commands and returns their combined output.
type Executor interface {
	ApplyFiles(ctx context.Context, role string, files []response.File) error
	RunCommands(ctx context.Context, role string, cmds []string) ([]string, error)
}

// Result is what a finished (or paused) run reports back: the state the task
// stopped in and, when paused, the questions a human has to answer.
type Result struct {
	State     state.State
	Questions []response.Question
}

// Orchestrator runs one task at a time. A machine is not reusable across
// tasks: build a new Orchestrator per task with New or NewWithBudget.
type Orchestrator struct {
	agent         Agent
	ex            Executor
	budget        *budget.Budget
	machine       *state.Machine
	lg            *log.Logger
	maxIterations int
}

// maxIterations bounds the number of state-machine steps a single run may
// take. It is a backstop against a planner that never converges: the normal
// loop control is the retry budget, not this counter.
const maxIterationsDefault = 100

// New returns an orchestrator with the default retry budget, a fresh machine
// in state.Request and a 100-step iteration cap.
func New(agent Agent, ex Executor, lg *log.Logger) *Orchestrator {
	if lg == nil {
		// A nil logger would panic on the first Logf; discard instead so a
		// caller that does not care about logging still gets a working run.
		lg = log.New(nil)
	}
	return &Orchestrator{
		agent:         agent,
		ex:            ex,
		budget:        budget.NewDefaultBudget(),
		machine:       state.New(),
		lg:            lg,
		maxIterations: maxIterationsDefault,
	}
}

// NewWithBudget is the test-friendly constructor: it uses a budget of exactly
// maxRetries instead of the default, and maxRetries == 0 yields an empty
// budget, so the first failed round pauses the task.
func NewWithBudget(agent Agent, ex Executor, lg *log.Logger, maxRetries int) *Orchestrator {
	o := New(agent, ex, lg)
	o.budget = budget.NewBudget(maxRetries)
	return o
}

// Run drives task from state.Request until the machine reaches Success, a
// pause is required, or an agent/executor error occurs. The returned error is
// the underlying error (wrapped), so errors.Is keeps working for callers.
func (o *Orchestrator) Run(ctx context.Context, task string) (Result, error) {
	for i := 0; i < o.maxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return Result{State: o.machine.State()}, err
		}

		o.lg.Logf(log.LevelInfo, "state=%s", o.machine.State())

		var (
			res  Result
			done bool
			err  error
		)

		switch o.machine.State() {
		case state.Request:
			err = o.apply(state.Start)
		case state.Planning:
			res, done, err = o.runPlanning(ctx, task)
		case state.DevQA:
			res, done, err = o.runDevQA(ctx, task)
		case state.Review:
			res, done, err = o.runReview(ctx, task)
		case state.Pause, state.Success:
			// Reached when a caller re-enters a finished machine; both are
			// reported as-is instead of being stepped again.
			return Result{State: o.machine.State()}, nil
		default:
			return Result{State: o.machine.State()},
				fmt.Errorf("orchestrator: unsupported state %q", o.machine.State())
		}

		if err != nil {
			return Result{State: o.machine.State()}, err
		}
		if done {
			return res, nil
		}
	}

	return Result{State: o.machine.State()},
		fmt.Errorf("orchestrator: task did not converge within %d iterations", o.maxIterations)
}

// runPlanning asks the planner for the plan and applies it. A plan that is not
// finished and not paused keeps the task in Planning for another turn.
func (o *Orchestrator) runPlanning(ctx context.Context, task string) (Result, bool, error) {
	resp, err := o.call(ctx, "planner", task)
	if err != nil {
		return Result{}, false, err
	}

	if res, paused, err := o.pauseIfNeeded(resp); err != nil || paused {
		return res, paused, err
	}

	if err := o.ex.ApplyFiles(ctx, "planner", resp.Files); err != nil {
		return Result{}, false, fmt.Errorf("orchestrator: apply planner files: %w", err)
	}

	if resp.Status == StatusPlanningDone {
		if err := o.apply(state.PlanDone); err != nil {
			return Result{}, false, err
		}
	}
	// Any other status means the plan is still incomplete: no event, so the
	// next iteration asks the planner again.
	return Result{}, false, nil
}

// runDevQA runs one developer/tester round and decides whether to converge to
// Review, spend a retry, or pause.
func (o *Orchestrator) runDevQA(ctx context.Context, task string) (Result, bool, error) {
	dResp, err := o.call(ctx, "developer", task)
	if err != nil {
		return Result{}, false, err
	}
	if res, paused, err := o.pauseIfNeeded(dResp); err != nil || paused {
		return res, paused, err
	}
	if err := o.ex.ApplyFiles(ctx, "developer", dResp.Files); err != nil {
		return Result{}, false, fmt.Errorf("orchestrator: apply developer files: %w", err)
	}

	tResp, err := o.call(ctx, "tester", task)
	if err != nil {
		return Result{}, false, err
	}
	if res, paused, err := o.pauseIfNeeded(tResp); err != nil || paused {
		return res, paused, err
	}
	if err := o.ex.ApplyFiles(ctx, "tester", tResp.Files); err != nil {
		return Result{}, false, fmt.Errorf("orchestrator: apply tester files: %w", err)
	}

	if tResp.Status == StatusPass {
		if err := o.apply(state.ReviewStart); err != nil {
			return Result{}, false, err
		}
		return Result{}, false, nil
	}

	// Not a pass, and not a pause request either (those were handled above):
	// the round failed, so another attempt costs one retry. An unrecognised
	// tester status lands here too - it is not a pass, and the budget rather
	// than the iteration cap should bound the loop.
	if o.budget.CanRetry() {
		o.budget.Consume()
		o.lg.Logf(log.LevelWarn, "devqa: round failed (status=%s), retrying with %d retries left",
			tResp.Status, o.budget.Remaining())
		return Result{}, false, nil
	}

	if err := o.apply(state.CriticalPause); err != nil {
		return Result{}, false, err
	}
	o.lg.Logf(log.LevelError, "devqa: retry budget exhausted after status=%s", tResp.Status)
	return Result{
		State:     o.machine.State(),
		Questions: []response.Question{{ID: 1, Text: "retry budget exhausted"}},
	}, true, nil
}

// runReview asks the planner to validate the result against the plan. Approval
// finishes the task; rejection sends it back to Planning.
func (o *Orchestrator) runReview(ctx context.Context, task string) (Result, bool, error) {
	resp, err := o.call(ctx, "planner", "review: "+task)
	if err != nil {
		return Result{}, false, err
	}
	if res, paused, err := o.pauseIfNeeded(resp); err != nil || paused {
		return res, paused, err
	}

	if resp.Status == StatusPlanningDone {
		if err := o.apply(state.ReviewOK); err != nil {
			return Result{}, false, err
		}
		return Result{State: o.machine.State()}, true, nil
	}

	if err := o.apply(state.Fail); err != nil {
		return Result{}, false, err
	}
	o.lg.Logf(log.LevelWarn, "review: rejected (status=%s), back to planning", resp.Status)
	return Result{}, false, nil
}

// call runs one agent turn, wrapping errors with the role so a failure names
// the agent that produced it.
func (o *Orchestrator) call(ctx context.Context, role, prompt string) (*response.Response, error) {
	o.lg.Logf(log.LevelInfo, "agent=%s start", role)
	resp, err := o.agent.Run(ctx, role, prompt)
	if err != nil {
		o.lg.Logf(log.LevelError, "agent=%s error: %v", role, err)
		return nil, fmt.Errorf("orchestrator: %s agent: %w", role, err)
	}
	if resp == nil {
		o.lg.Logf(log.LevelError, "agent=%s returned nil response", role)
		return nil, fmt.Errorf("orchestrator: %s agent: %w", role, ErrNilResponse)
	}
	o.lg.Logf(log.LevelInfo, "agent=%s status=%s files=%d questions=%d",
		role, resp.Status, len(resp.Files), len(resp.Questions))
	return resp, nil
}

// pauseIfNeeded suspends the machine when the response asks for human input:
// either it carries questions, or it reports blocked/needs_pause. The paused
// Result carries the questions straight through to the caller.
func (o *Orchestrator) pauseIfNeeded(resp *response.Response) (Result, bool, error) {
	if len(resp.Questions) == 0 && !needsPause(resp.Status) {
		return Result{}, false, nil
	}

	if err := o.apply(state.CriticalPause); err != nil {
		return Result{}, false, err
	}
	o.lg.Logf(log.LevelWarn, "paused: status=%s questions=%d", resp.Status, len(resp.Questions))
	return Result{State: o.machine.State(), Questions: resp.Questions}, true, nil
}

// needsPause reports whether status is one of the statuses that suspend a task.
func needsPause(status string) bool {
	return status == StatusNeedsPause || status == StatusBlocked
}

// apply drives the machine with ev and logs the transition. Errors are wrapped
// so an illegal transition says which event caused it.
func (o *Orchestrator) apply(ev state.Event) error {
	if _, err := o.machine.Apply(ev); err != nil {
		return fmt.Errorf("orchestrator: apply %s from %s: %w", ev, o.machine.State(), err)
	}
	o.lg.Logf(log.LevelDebug, "event=%s state=%s", ev, o.machine.State())
	return nil
}
