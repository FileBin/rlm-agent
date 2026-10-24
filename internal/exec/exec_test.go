package exec

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// Compile-time proof that both implementations satisfy the interface the
// orchestrator programs against.
var (
	_ Runner = ContainerRunner{}
	_ Runner = (*FakeRunner)(nil)
)

func TestBuildArgs(t *testing.T) {
	r := ContainerRunner{Container: "factory"}

	got := r.buildArgs("developer", "nix", "flake", "check")
	want := []string{"run", "factory", "--", "runuser", "-u", "developer", "--", "nix", "flake", "check"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildArgsKeepsPayloadVerbatim(t *testing.T) {
	r := ContainerRunner{Container: "factory"}

	// Flags that look like nixos-container options must land after "--" so
	// neither nixos-container nor runuser can interpret them.
	got := r.buildArgs("tester", "-x", "--role=root", "a b")
	want := []string{"run", "factory", "--", "runuser", "-u", "tester", "--", "-x", "--role=root", "a b"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs() = %#v, want %#v", got, want)
	}
}

func TestRunWithoutCommandFails(t *testing.T) {
	r := ContainerRunner{Container: "factory"}
	if _, _, err := r.Run(context.Background(), "developer"); err == nil {
		t.Fatal("Run() with no command = nil error, want an error")
	}
}

// Call is one recorded Run invocation.
type Call struct {
	Role string
	Args []string
}

// FakeRunner is a Runner that records calls and replays canned results. It
// lets the orchestrator be tested without a container or a nix daemon.
type FakeRunner struct {
	Stdout string
	Stderr string
	Err    error

	mu    sync.Mutex
	calls []Call
}

// Run records the invocation and returns the canned result.
func (f *FakeRunner) Run(_ context.Context, role string, args ...string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Copy the payload: the caller's slice must not alias recorded history.
	f.calls = append(f.calls, Call{Role: role, Args: append([]string(nil), args...)})
	return f.Stdout, f.Stderr, f.Err
}

// Calls returns the recorded invocations in order.
func (f *FakeRunner) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

// TestFakeRunnerAsRunner drives a FakeRunner through the interface, the way
// the orchestrator will use a Runner, and checks the recorded calls.
func TestFakeRunnerAsRunner(t *testing.T) {
	sentinel := errors.New("exit status 1")
	fake := &FakeRunner{
		Stdout: "checked 3 flake outputs\n",
		Stderr: "error: flake attribute missing\n",
		Err:    sentinel,
	}

	// Deliberately declared as the interface, not the concrete type.
	var runner Runner = fake

	stdout, stderr, err := runner.Run(context.Background(), "developer", "nix", "flake", "check")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run() error = %v, want %v", err, sentinel)
	}
	if want := "checked 3 flake outputs\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	if want := "error: flake attribute missing\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}

	// A successful retry: the orchestrator reruns a command after fixing it.
	// Only the test goroutine touches Err, so no locking is needed here.
	fake.Err = nil
	fake.Stderr = ""
	if _, _, err := runner.Run(context.Background(), "tester", "nix", "build"); err != nil {
		t.Fatalf("second Run() error = %v, want nil", err)
	}

	want := []Call{
		{Role: "developer", Args: []string{"nix", "flake", "check"}},
		{Role: "tester", Args: []string{"nix", "build"}},
	}
	if got := fake.Calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Calls() = %#v, want %#v", got, want)
	}
}

// TestFakeRunnerCopiesArgs guards the recorded history against a caller that
// mutates its argv after the call.
func TestFakeRunnerCopiesArgs(t *testing.T) {
	fake := &FakeRunner{}

	args := []string{"nix", "flake", "check"}
	if _, _, err := fake.Run(context.Background(), "developer", args...); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	args[0] = "rm"

	if got := fake.Calls()[0].Args[0]; got != "nix" {
		t.Fatalf("recorded arg = %q, want %q", got, "nix")
	}
}
