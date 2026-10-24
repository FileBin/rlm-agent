package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"rlm-agent/internal/log"
	"rlm-agent/internal/response"
	"rlm-agent/internal/state"
)

// newTestLogger returns a logger writing into a buffer. The buffer is only
// read after the orchestrator finished, and Close in Cleanup flushes the
// writer goroutine, so no synchronisation is needed in the tests.
func newTestLogger(t *testing.T) (*log.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	lg := log.New(&buf)
	t.Cleanup(func() { _ = lg.Close() })
	return lg, &buf
}

// agentCall records one Run invocation, so tests can assert on roles and
// prompts without inspecting log output.
type agentCall struct {
	role   string
	prompt string
}

// fakeAgent replays scripted responses. Responses are served per role first
// (so a planner script covers both plan and review turns), then from the
// shared queue. When a script runs dry it errors instead of inventing an
// answer, which turns a too-short script into an obvious failure.
type fakeAgent struct {
	byRole map[string][]*response.Response
	queue  []*response.Response
	err    error
	calls  []agentCall
}

func newFakeAgent(byRole map[string][]*response.Response) *fakeAgent {
	return &fakeAgent{byRole: byRole}
}

func (f *fakeAgent) Run(_ context.Context, role, prompt string) (*response.Response, error) {
	f.calls = append(f.calls, agentCall{role: role, prompt: prompt})

	if f.err != nil {
		return nil, f.err
	}
	if q := f.byRole[role]; len(q) > 0 {
		f.byRole[role] = q[1:]
		return q[0], nil
	}
	if len(f.queue) > 0 {
		resp := f.queue[0]
		f.queue = f.queue[1:]
		return resp, nil
	}
	return nil, fmt.Errorf("fakeAgent: no scripted response for role %q (prompt %q)", role, prompt)
}

// roles returns the role of every call in order.
func (f *fakeAgent) roles() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.role
	}
	return out
}

// applyCall records one ApplyFiles invocation.
type applyCall struct {
	role  string
	files []response.File
}

// fakeExecutor records applied file batches and returns err, if set.
// RunCommands is part of the interface but the orchestration core never calls
// it, so it reports that loudly instead of pretending to run anything.
type fakeExecutor struct {
	applied []applyCall
	err     error
}

func (f *fakeExecutor) ApplyFiles(_ context.Context, role string, files []response.File) error {
	f.applied = append(f.applied, applyCall{role: role, files: files})
	return f.err
}

func (f *fakeExecutor) RunCommands(_ context.Context, role string, cmds []string) ([]string, error) {
	return nil, fmt.Errorf("fakeExecutor: RunCommands(%q) unexpectedly called with %d commands", role, len(cmds))
}

// appliedRoles returns the role of every ApplyFiles call in order.
func (f *fakeExecutor) appliedRoles() []string {
	out := make([]string, len(f.applied))
	for i, c := range f.applied {
		out[i] = c.role
	}
	return out
}

// resp builds a response with one file so ApplyFiles is visibly exercised.
func resp(status string) *response.Response {
	return &response.Response{
		Status: status,
		Files:  []response.File{{Path: "out/" + status + ".go", Content: "// " + status}},
	}
}

// respWithFiles builds a response carrying the given paths.
func respWithFiles(status string, paths ...string) *response.Response {
	files := make([]response.File, len(paths))
	for i, p := range paths {
		files[i] = response.File{Path: p, Content: "content"}
	}
	return &response.Response{Status: status, Files: files}
}

func equalRoles(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestRunHappyPath walks Request -> Planning -> DevQA -> Review -> Success and
// checks that each agent was called with the right role and that every file
// batch was applied.
func TestRunHappyPath(t *testing.T) {
	lg, buf := newTestLogger(t)

	ag := newFakeAgent(map[string][]*response.Response{
		"planner": {
			respWithFiles(StatusPlanningDone, "plan/todo.md", "plan/architecture.md"),
			respWithFiles(StatusPlanningDone, "plan/review.md"),
		},
		"developer": {respWithFiles(StatusCodeDone, "src/main.go")},
		"tester":    {respWithFiles(StatusPass, "test/main_test.go")},
	})
	ex := &fakeExecutor{}

	o := New(ag, ex, lg)
	got, err := o.Run(context.Background(), "build the thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.State != state.Success {
		t.Fatalf("Run() state = %q, want %q", got.State, state.Success)
	}
	if len(got.Questions) != 0 {
		t.Errorf("Run() questions = %v, want none", got.Questions)
	}

	wantRoles := []string{"planner", "developer", "tester", "planner"}
	if gotRoles := ag.roles(); !equalRoles(gotRoles, wantRoles) {
		t.Errorf("agent calls = %v, want %v", gotRoles, wantRoles)
	}
	if first := ag.calls[0]; first.prompt != "build the thing" {
		t.Errorf("planning prompt = %q, want the task", first.prompt)
	}
	if last := ag.calls[len(ag.calls)-1]; last.prompt != "review: build the thing" {
		t.Errorf("review prompt = %q, want %q", last.prompt, "review: build the thing")
	}

	wantApplied := []string{"planner", "developer", "tester"}
	if gotApplied := ex.appliedRoles(); !equalRoles(gotApplied, wantApplied) {
		t.Errorf("applied roles = %v, want %v", gotApplied, wantApplied)
	}
	if len(ex.applied[0].files) != 2 {
		t.Errorf("planner files = %d, want 2", len(ex.applied[0].files))
	}
	// The logger writes from its own goroutine; Close drains the queue before
	// the buffer is inspected. Close is idempotent, so Cleanup is unaffected.
	if err := lg.Close(); err != nil {
		t.Fatalf("logger Close() error = %v", err)
	}
	if !strings.Contains(buf.String(), "state=Success") {
		t.Errorf("log does not record the final state:\n%s", buf.String())
	}
}

// TestRunPausesOnQuestions covers the human-in-the-loop exit from Planning:
// questions suspend the task before any file is applied.
func TestRunPausesOnQuestions(t *testing.T) {
	lg, _ := newTestLogger(t)

	questions := []response.Question{
		{ID: 1, Text: "which database should be used?"},
		{ID: 2, Text: "is the API public?"},
	}
	ag := newFakeAgent(map[string][]*response.Response{
		"planner": {{Status: StatusPlanningDone, Questions: questions}},
	})
	ex := &fakeExecutor{}

	got, err := New(ag, ex, lg).Run(context.Background(), "build the thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.State != state.Pause {
		t.Fatalf("Run() state = %q, want %q", got.State, state.Pause)
	}
	if len(got.Questions) != len(questions) {
		t.Fatalf("Run() questions = %d, want %d", len(got.Questions), len(questions))
	}
	for i, q := range questions {
		if got.Questions[i] != q {
			t.Errorf("question[%d] = %+v, want %+v", i, got.Questions[i], q)
		}
	}
	if len(ex.applied) != 0 {
		t.Errorf("files applied while paused: %v", ex.appliedRoles())
	}
	if len(ag.calls) != 1 {
		t.Errorf("agent calls = %d, want 1 (no second planner turn after a pause)", len(ag.calls))
	}
}

// TestRunRetriesAfterFail is the fail -> retry -> pass path: one budget unit is
// spent and the task still reaches Success.
func TestRunRetriesAfterFail(t *testing.T) {
	lg, _ := newTestLogger(t)

	ag := newFakeAgent(map[string][]*response.Response{
		"planner": {
			resp(StatusPlanningDone),
			resp(StatusPlanningDone), // review approves
		},
		"developer": {resp(StatusCodeDone), resp(StatusCodeDone)},
		"tester":    {resp(StatusFail), resp(StatusPass)},
	})
	ex := &fakeExecutor{}

	o := NewWithBudget(ag, ex, lg, 2)
	before := o.budget.Remaining()

	got, err := o.Run(context.Background(), "build the thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.State != state.Success {
		t.Fatalf("Run() state = %q, want %q", got.State, state.Success)
	}
	if after := o.budget.Remaining(); after != before-1 {
		t.Errorf("budget remaining = %d, want %d", after, before-1)
	}

	wantRoles := []string{"planner", "developer", "tester", "developer", "tester", "planner"}
	if gotRoles := ag.roles(); !equalRoles(gotRoles, wantRoles) {
		t.Errorf("agent calls = %v, want %v", gotRoles, wantRoles)
	}
}

// TestRunPausesWhenBudgetExhausted checks that an empty budget turns a tester
// failure into a pause carrying a budget question, not an error.
func TestRunPausesWhenBudgetExhausted(t *testing.T) {
	lg, _ := newTestLogger(t)

	ag := newFakeAgent(map[string][]*response.Response{
		"planner":   {resp(StatusPlanningDone)},
		"developer": {resp(StatusCodeDone)},
		"tester":    {resp(StatusFail)},
	})
	ex := &fakeExecutor{}

	o := NewWithBudget(ag, ex, lg, 0)
	got, err := o.Run(context.Background(), "build the thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got.State != state.Pause {
		t.Fatalf("Run() state = %q, want %q", got.State, state.Pause)
	}
	if len(got.Questions) != 1 {
		t.Fatalf("Run() questions = %v, want exactly one budget question", got.Questions)
	}
	if !strings.Contains(got.Questions[0].Text, "budget") {
		t.Errorf("question = %q, want it to mention the budget", got.Questions[0].Text)
	}
	if got.Questions[0].ID != 1 {
		t.Errorf("question id = %d, want 1", got.Questions[0].ID)
	}
	if o.budget.Remaining() != 0 {
		t.Errorf("budget remaining = %d, want 0", o.budget.Remaining())
	}
	// The failed round must not loop: exactly one developer/tester pair ran.
	wantRoles := []string{"planner", "developer", "tester"}
	if gotRoles := ag.roles(); !equalRoles(gotRoles, wantRoles) {
		t.Errorf("agent calls = %v, want %v", gotRoles, wantRoles)
	}
}

// TestRunAgentError makes sure an agent failure surfaces unchanged (wrapped,
// so errors.Is still matches) and stops the run.
func TestRunAgentError(t *testing.T) {
	lg, _ := newTestLogger(t)

	errBoom := errors.New("boom")
	ag := newFakeAgent(map[string][]*response.Response{
		"planner": {resp(StatusPlanningDone)},
	})
	ag.err = errBoom

	got, err := New(ag, &fakeExecutor{}, lg).Run(context.Background(), "build the thing")
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run() error = %v, want %v", err, errBoom)
	}
	if got.State != state.Planning {
		t.Errorf("Run() state = %q, want %q (planning is where it failed)", got.State, state.Planning)
	}
}

// TestRunExecutorError checks that a file-apply failure aborts the run.
func TestRunExecutorError(t *testing.T) {
	lg, _ := newTestLogger(t)

	errBoom := errors.New("apply failed")
	ag := newFakeAgent(map[string][]*response.Response{
		"planner": {resp(StatusPlanningDone)},
	})
	ex := &fakeExecutor{err: errBoom}

	got, err := New(ag, ex, lg).Run(context.Background(), "build the thing")
	if !errors.Is(err, errBoom) {
		t.Fatalf("Run() error = %v, want %v", err, errBoom)
	}
	if got.State != state.Planning {
		t.Errorf("Run() state = %q, want %q", got.State, state.Planning)
	}
	if len(ex.applied) != 1 {
		t.Errorf("ApplyFiles calls = %d, want 1", len(ex.applied))
	}
}
