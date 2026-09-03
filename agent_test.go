package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

type fakeRuntime struct {
	codespaces            []Codespace
	session               *fakeSession
	startErr              error
	status                CodespaceStatus
	statusErr             error
	started               CodespaceStatus
	startErr2             error
	stopped               CodespaceStatus
	stopErr               error
	startEntered          chan struct{}
	blockStart            bool
	codespaceStartEntered chan struct{}
	blockCodespaceStart   bool
}

type blockingCloseRuntime struct {
	closeTimeout time.Duration
}

type pendingForceRuntime struct {
	closeTimeout time.Duration
	allowForce   <-chan struct{}
}

type blockingListRuntime struct {
	active    atomic.Int32
	maxActive atomic.Int32
}

func (r *blockingListRuntime) ListCodespaces(ctx context.Context) ([]Codespace, error) {
	active := r.active.Add(1)
	defer r.active.Add(-1)
	for {
		maxActive := r.maxActive.Load()
		if active <= maxActive || r.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*blockingListRuntime) CodespaceStatus(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (*blockingListRuntime) StartCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (*blockingListRuntime) StopCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (*blockingListRuntime) Start(context.Context, AgentStart) (RuntimeSession, error) {
	return nil, errors.New("unexpected agent start")
}

func (r *blockingCloseRuntime) ListCodespaces(context.Context) ([]Codespace, error) {
	return nil, nil
}

func (r *blockingCloseRuntime) CodespaceStatus(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *blockingCloseRuntime) StartCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *blockingCloseRuntime) StopCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *blockingCloseRuntime) Start(_ context.Context, spec AgentStart) (RuntimeSession, error) {
	forced := make(chan struct{})
	return &sdkRuntimeSession{
		session:     &copilot.Session{SessionID: spec.AgentID},
		events:      newNormalizedEventSink(),
		unsubscribe: func() {},
		disconnect: func() error {
			<-forced
			return nil
		},
		stop: func() error { return nil },
		forceStop: func() {
			close(forced)
		},
		serverClose:  func() {},
		closeTimeout: r.closeTimeout,
		terminated:   make(chan struct{}),
	}, nil
}

func (r *pendingForceRuntime) ListCodespaces(context.Context) ([]Codespace, error) {
	return nil, nil
}

func (r *pendingForceRuntime) CodespaceStatus(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *pendingForceRuntime) StartCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *pendingForceRuntime) StopCodespace(context.Context, string) (CodespaceStatus, error) {
	return CodespaceStatus{}, nil
}

func (r *pendingForceRuntime) Start(_ context.Context, spec AgentStart) (RuntimeSession, error) {
	return &sdkRuntimeSession{
		session:     &copilot.Session{SessionID: spec.AgentID},
		events:      newNormalizedEventSink(),
		unsubscribe: func() {},
		disconnect: func() error {
			<-r.allowForce
			return nil
		},
		stop: func() error { return nil },
		forceStop: func() {
			<-r.allowForce
		},
		serverClose:  func() {},
		closeTimeout: r.closeTimeout,
		terminated:   make(chan struct{}),
	}, nil
}

func (r *fakeRuntime) ListCodespaces(context.Context) ([]Codespace, error) {
	return r.codespaces, nil
}

func (r *fakeRuntime) CodespaceStatus(context.Context, string) (CodespaceStatus, error) {
	return r.status, r.statusErr
}

func (r *fakeRuntime) StartCodespace(ctx context.Context, _ string) (CodespaceStatus, error) {
	if r.codespaceStartEntered != nil {
		close(r.codespaceStartEntered)
	}
	if r.blockCodespaceStart {
		<-ctx.Done()
		return CodespaceStatus{}, ctx.Err()
	}
	return r.started, r.startErr2
}

func (r *fakeRuntime) StopCodespace(context.Context, string) (CodespaceStatus, error) {
	return r.stopped, r.stopErr
}

func (r *fakeRuntime) Start(ctx context.Context, _ AgentStart) (RuntimeSession, error) {
	if r.startEntered != nil {
		close(r.startEntered)
	}
	if r.blockStart {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if r.startErr != nil {
		return nil, r.startErr
	}
	return r.session, nil
}

func newTestSupervisor(t *testing.T, runtime RuntimeFactory) *Supervisor {
	t.Helper()
	return newSupervisor(runtime, newLeaseManager(t.TempDir()))
}

type fakeSession struct {
	id     string
	events chan NormalizedEvent
	done   chan error

	mu       sync.Mutex
	prompts  []string
	closed   int
	doneOpen bool
	closeErr error
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type blockingProtocolWriter struct {
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingProtocolWriter() *blockingProtocolWriter {
	return &blockingProtocolWriter{
		entered: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (w *blockingProtocolWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.closed
	return 0, io.ErrClosedPipe
}

func (w *blockingProtocolWriter) Close() error {
	w.once.Do(func() { close(w.entered) })
	close(w.closed)
	return nil
}

type terminalEventFailureWriter struct {
	err          error
	terminalSeen chan struct{}
	closed       chan struct{}
	terminalOnce sync.Once
	closeOnce    sync.Once
}

func newTerminalEventFailureWriter(err error) *terminalEventFailureWriter {
	return &terminalEventFailureWriter{
		err:          err,
		terminalSeen: make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (w *terminalEventFailureWriter) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte(`"state":"stopped"`)) {
		w.terminalOnce.Do(func() { close(w.terminalSeen) })
		return 0, w.err
	}
	return len(data), nil
}

func (w *terminalEventFailureWriter) Close() error {
	w.closeOnce.Do(func() { close(w.closed) })
	return nil
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		id:       "session-1",
		events:   make(chan NormalizedEvent, 32),
		done:     make(chan error, 1),
		doneOpen: true,
	}
}

func (s *fakeSession) ID() string {
	return s.id
}

func (s *fakeSession) Send(_ context.Context, prompt string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompts = append(s.prompts, prompt)
	return nil
}

func (s *fakeSession) Events() <-chan NormalizedEvent {
	return s.events
}

func (s *fakeSession) Done() <-chan error {
	return s.done
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed++
	if s.closed == 1 {
		close(s.events)
		if s.doneOpen {
			close(s.done)
			s.doneOpen = false
		}
	}
	return s.closeErr
}

func (s *fakeSession) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed != 0 {
		return
	}
	s.done <- err
	close(s.done)
	s.doneOpen = false
}

func (s *fakeSession) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func TestParseProtocolRequest_Strict(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "missing id", line: `{"method":"agent.status","params":{"agent_id":"a"}}`},
		{name: "unknown request field", line: `{"id":"1","method":"shutdown","params":{},"extra":true}`},
		{name: "non-object params", line: `{"id":"1","method":"shutdown","params":[]}`},
		{name: "two objects", line: `{"id":"1","method":"shutdown","params":{}} {}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseProtocolRequest([]byte(test.line)); err == nil {
				t.Fatal("parseProtocolRequest() succeeded")
			}
		})
	}
}

func TestDispatchProtocolRequest_UsesStableErrorCodes(t *testing.T) {
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	tests := []struct {
		name string
		line string
		code string
	}{
		{name: "unknown method", line: `{"id":"1","method":"unknown","params":{}}`, code: "method_not_found"},
		{name: "unknown params", line: `{"id":"2","method":"shutdown","params":{"extra":true}}`, code: "invalid_params"},
		{name: "missing agent", line: `{"id":"3","method":"agent.status","params":{"agent_id":"missing"}}`, code: "agent_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := dispatchProtocolRequest(context.Background(), []byte(test.line), supervisor)
			if response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("response = %+v, want %q", response, test.code)
			}
		})
	}
}

func TestIsAgentServeCommand(t *testing.T) {
	if !isAgentServeCommand([]string{"agent", "serve"}) {
		t.Fatal("agent serve command was not detected")
	}
	if isAgentServeCommand([]string{"agent", "serve", "extra"}) {
		t.Fatal("agent serve command accepted extra arguments")
	}
}

func TestRemoteCopilotArgs_UsesAgentAuthSocket(t *testing.T) {
	server := &ServerConfig{SocketPath: "/tmp/ado-auth-agent.sock", Port: 1234}
	got := remoteCopilotArgs("space", server)
	want := []string{
		"codespace", "ssh", "--codespace", "space", "--", "-T", "-R",
		"/tmp/ado-auth-agent.sock:127.0.0.1:1234",
		"env", "GH_ADO_CODESPACES_AUTH_SOCKET=/tmp/ado-auth-agent.sock", "copilot",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("remoteCopilotArgs() = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "github_pat_") {
		t.Fatalf("remoteCopilotArgs() exposed a secret: %q", got)
	}
}

func TestSupervisor_LifecycleSequencesEvents(t *testing.T) {
	session := newFakeSession()
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})
	ctx := context.Background()

	started, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "space"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if started.State != AgentIdle || started.SessionID != "session-1" {
		t.Fatalf("Start() = %+v", started)
	}
	if _, err := supervisor.Send(ctx, "agent-1", "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	session.events <- NormalizedEvent{Kind: "assistant.message", Text: "hello back"}
	session.events <- NormalizedEvent{Kind: "assistant.idle"}

	eventually(t, func() bool {
		status, err := supervisor.Status(ctx, "agent-1")
		return err == nil && status.State == AgentIdle
	})
	stopped, err := supervisor.Stop(ctx, "agent-1")
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if stopped.State != AgentStopped {
		t.Fatalf("Stop() state = %q", stopped.State)
	}

	var events []protocolEvent
	for len(supervisor.events) > 0 {
		events = append(events, <-supervisor.events)
	}
	if len(events) < 7 {
		t.Fatalf("event count = %d, want at least 7", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
	}
	if events[3].Event.Kind != "assistant.message" {
		t.Fatalf("runtime event kind = %q", events[3].Event.Kind)
	}
}

func TestSupervisor_StopIsIdempotent(t *testing.T) {
	session := newFakeSession()
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})
	ctx := context.Background()
	if _, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	first, err := supervisor.Stop(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := supervisor.Stop(ctx, "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || session.closeCount() != 1 {
		t.Fatalf("stops = %+v and %+v, close count = %d", first, second, session.closeCount())
	}
	if err := supervisor.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestSupervisor_StoppedAgentRejectsPrompt(t *testing.T) {
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatal(err)
	}

	_, err := supervisor.Send(context.Background(), "agent-1", "ignored")
	var protocolErr *protocolErrorValue
	if !errors.As(err, &protocolErr) || protocolErr.code != "agent_stopped" {
		t.Fatalf("Send() error = %v, want agent_stopped", err)
	}
}

func TestSupervisor_ContextCancellationRecordsCleanupFailure(t *testing.T) {
	session := newFakeSession()
	session.closeErr = errors.New("token=secret")
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	cancel()

	eventually(t, func() bool {
		status, err := supervisor.Status(context.Background(), "agent-1")
		return err == nil && status.State == AgentFailed
	})
	status, err := supervisor.Status(context.Background(), "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.SafeError == "" || strings.Contains(status.SafeError, "secret") {
		t.Fatalf("status = %+v", status)
	}
	if session.closeCount() != 1 {
		t.Fatalf("close count = %d, want 1", session.closeCount())
	}
}

func TestSupervisor_StopReturnsCleanupFailureOnce(t *testing.T) {
	closeErr := errors.New("close failed")
	session := newFakeSession()
	session.closeErr = closeErr
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})

	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	stopped, err := supervisor.Stop(context.Background(), "agent-1")
	if !errors.Is(err, closeErr) {
		t.Fatalf("Stop() error = %v, want %v", err, closeErr)
	}
	if stopped.State != AgentFailed || stopped.SafeError != closeErr.Error() {
		t.Fatalf("Stop() snapshot = %+v", stopped)
	}
	if session.closeCount() != 1 {
		t.Fatalf("close count = %d, want 1", session.closeCount())
	}
}

func TestAgentPermissionHandler(t *testing.T) {
	managedApprovalRequired := true
	tests := []struct {
		name       string
		approveAll bool
		managed    bool
		request    copilot.PermissionRequest
		wantKind   string
	}{
		{
			name:       "no remote user",
			approveAll: false,
			request:    &copilot.PermissionRequestRead{},
			wantKind:   "user-not-available",
		},
		{
			name:       "ordinary request",
			approveAll: true,
			request:    &copilot.PermissionRequestRead{},
			wantKind:   "approve-once",
		},
		{
			name:       "managed approval request",
			approveAll: true,
			request:    &copilot.PermissionRequestRead{ManagedApprovalRequired: &managedApprovalRequired},
			wantKind:   "reject",
		},
		{
			name:       "managed settings enabled",
			approveAll: true,
			managed:    true,
			request:    &copilot.PermissionRequestRead{},
			wantKind:   "user-not-available",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invocation := copilot.PermissionInvocation{
				ManagedSettingsEnabled: test.managed,
			}
			decision, err := agentPermissionHandler(test.approveAll)(test.request, invocation)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(decision.Kind()); got != test.wantKind {
				t.Fatalf("decision kind = %q, want %q", got, test.wantKind)
			}
		})
	}
}

func TestCodespaceRuntime_ListCodespacesUsesContextualRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotExecutable string
	var gotArgs []string
	runtime := codespaceRuntime{
		runCommand: func(gotCtx context.Context, executable string, args ...string) ([]byte, []byte, error) {
			if gotCtx != ctx {
				t.Fatal("ListCodespaces did not pass its context to the runner")
			}
			gotExecutable = executable
			gotArgs = args
			return []byte(`[{"name":"space"}]`), nil, nil
		},
	}
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()

	codespaces, err := runtime.ListCodespaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(codespaces) != 1 || codespaces[0].Name != "space" {
		t.Fatalf("codespaces = %+v", codespaces)
	}
	if gotExecutable != "/test/gh" {
		t.Fatalf("executable = %q", gotExecutable)
	}
	wantArgs := []string{"codespace", "list", "--json", "name,displayName,repository,gitStatus,state,lastUsedAt"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("arguments = %q, want %q", gotArgs, wantArgs)
	}
}

func TestRunCodespaceCommand_StopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := runCodespaceCommand(ctx, os.Args[0], "-test.run=^$")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runCodespaceCommand() error = %v, want context cancellation", err)
	}
}

func TestParseCodespaceStatus_MapsStates(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  CodespaceState
	}{
		{name: "available", input: `{"name":"space","state":"Available"}`, want: CodespaceAvailable},
		{name: "starting", input: `{"name":"space","state":"Starting"}`, want: CodespaceStarting},
		{name: "pending", input: `{"name":"space","state":"Pending"}`, want: CodespaceStarting},
		{name: "stopping", input: `{"name":"space","state":"Stopping"}`, want: CodespaceStopping},
		{name: "shutting down", input: `{"name":"space","state":"ShuttingDown"}`, want: CodespaceStopping},
		{name: "rebuilding", input: `{"name":"space","state":"Rebuilding"}`, want: CodespaceRebuilding},
		{name: "shutdown", input: `{"name":"space","state":"Shutdown"}`, want: CodespaceShutdown},
		{name: "unknown", input: `{"name":"space","state":"Archived"}`, want: CodespaceUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, err := parseCodespaceStatus([]byte(test.input))
			if err != nil {
				t.Fatal(err)
			}
			if status != (CodespaceStatus{Name: "space", State: test.want}) {
				t.Fatalf("status = %+v, want state %q", status, test.want)
			}
		})
	}
}

func TestCodespaceRuntime_CommandsAndIdempotency(t *testing.T) {
	tests := []struct {
		name      string
		operation func(context.Context, codespaceRuntime) (CodespaceStatus, error)
		responses []string
		wantCalls [][]string
		want      CodespaceStatus
	}{
		{
			name: "status",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.CodespaceStatus(ctx, "space")
			},
			responses: []string{`{"name":"space","state":"Available"}`},
			wantCalls: [][]string{{"codespace", "view", "--codespace", "space", "--json", "name,state"}},
			want:      CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "start available",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{`{"name":"space","state":"Available"}`},
			wantCalls: [][]string{{"codespace", "view", "--codespace", "space", "--json", "name,state"}},
			want:      CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "start starting",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{`{"name":"space","state":"Starting"}`, `{"name":"space","state":"Available"}`},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "start shutdown",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{`{"name":"space","state":"Shutdown"}`, `{}`, `{"name":"space","state":"Available"}`},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"api", "-X", "POST", "/user/codespaces/space/start"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "immediate stop start race",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{
				`{"name":"space","state":"Stopping"}`,
				`{"name":"space","state":"Shutdown"}`,
				`{}`,
				`{"name":"space","state":"Available"}`,
			},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"api", "-X", "POST", "/user/codespaces/space/start"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "stopping becomes available through another start",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{
				`{"name":"space","state":"Stopping"}`,
				`{"name":"space","state":"Starting"}`,
				`{"name":"space","state":"Available"}`,
			},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "start rebuilding",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			responses: []string{
				`{"name":"space","state":"Rebuilding"}`,
				`{"name":"space","state":"Available"}`,
			},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceAvailable},
		},
		{
			name: "stop shutdown",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StopCodespace(ctx, "space")
			},
			responses: []string{`{"name":"space","state":"Shutdown"}`},
			wantCalls: [][]string{{"codespace", "view", "--codespace", "space", "--json", "name,state"}},
			want:      CodespaceStatus{Name: "space", State: CodespaceShutdown},
		},
		{
			name: "stop available",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StopCodespace(ctx, "space")
			},
			responses: []string{
				`{"name":"space","state":"Available"}`,
				`{}`,
				`{"name":"space","state":"Stopping"}`,
				`{"name":"space","state":"Shutdown"}`,
			},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "stop", "--codespace", "space"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceShutdown},
		},
		{
			name: "stop stopping",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StopCodespace(ctx, "space")
			},
			responses: []string{
				`{"name":"space","state":"Stopping"}`,
				`{"name":"space","state":"Shutdown"}`,
			},
			wantCalls: [][]string{
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
				{"codespace", "view", "--codespace", "space", "--json", "name,state"},
			},
			want: CodespaceStatus{Name: "space", State: CodespaceShutdown},
		},
	}
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls [][]string
			response := 0
			runtime := codespaceRuntime{
				pollInterval: time.Millisecond,
				runCommand: func(ctx context.Context, executable string, args ...string) ([]byte, []byte, error) {
					if ctx.Err() != nil {
						return nil, nil, ctx.Err()
					}
					if executable != "/test/gh" {
						t.Fatalf("executable = %q", executable)
					}
					calls = append(calls, append([]string(nil), args...))
					stdout := []byte(test.responses[response])
					response++
					return stdout, nil, nil
				},
			}
			status, err := test.operation(context.Background(), runtime)
			if err != nil {
				t.Fatal(err)
			}
			if status != test.want {
				t.Fatalf("status = %+v, want %+v", status, test.want)
			}
			if len(calls) != len(test.wantCalls) {
				t.Fatalf("calls = %q, want %q", calls, test.wantCalls)
			}
			for index := range calls {
				if strings.Join(calls[index], "\x00") != strings.Join(test.wantCalls[index], "\x00") {
					t.Fatalf("call %d = %q, want %q", index, calls[index], test.wantCalls[index])
				}
			}
		})
	}
}

func TestCodespaceRuntime_StartCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	enteredPoll := make(chan struct{})
	calls := 0
	runtime := codespaceRuntime{
		pollInterval: time.Millisecond,
		runCommand: func(ctx context.Context, _ string, _ ...string) ([]byte, []byte, error) {
			calls++
			if calls == 1 {
				return []byte(`{"name":"space","state":"Starting"}`), nil, nil
			}
			close(enteredPoll)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runtime.StartCodespace(ctx, "space")
		done <- err
	}()
	select {
	case <-enteredPoll:
	case <-time.After(time.Second):
		t.Fatal("StartCodespace() did not begin polling")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("StartCodespace() error = %v, want context cancellation", err)
	}
}

func TestCodespaceRuntime_StartTimeout(t *testing.T) {
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	runtime := codespaceRuntime{
		pollInterval: time.Millisecond,
		pollTimeout:  time.Millisecond,
		runCommand: func(context.Context, string, ...string) ([]byte, []byte, error) {
			return []byte(`{"name":"space","state":"Starting"}`), nil, nil
		},
	}
	_, err := runtime.StartCodespace(context.Background(), "space")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartCodespace() error = %v, want timeout", err)
	}
}

func TestCodespaceRuntime_StopCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	enteredPoll := make(chan struct{})
	calls := 0
	runtime := codespaceRuntime{
		pollInterval: time.Millisecond,
		runCommand: func(ctx context.Context, _ string, _ ...string) ([]byte, []byte, error) {
			calls++
			switch calls {
			case 1:
				return []byte(`{"name":"space","state":"Available"}`), nil, nil
			case 2:
				return nil, nil, nil
			default:
				close(enteredPoll)
				<-ctx.Done()
				return nil, nil, ctx.Err()
			}
		},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runtime.StopCodespace(ctx, "space")
		done <- err
	}()
	select {
	case <-enteredPoll:
	case <-time.After(time.Second):
		t.Fatal("StopCodespace() did not begin polling")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("StopCodespace() error = %v, want context cancellation", err)
	}
}

func TestCodespaceRuntime_StopTimeout(t *testing.T) {
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	calls := 0
	runtime := codespaceRuntime{
		pollInterval: time.Millisecond,
		pollTimeout:  time.Millisecond,
		runCommand: func(context.Context, string, ...string) ([]byte, []byte, error) {
			calls++
			if calls == 1 {
				return []byte(`{"name":"space","state":"Available"}`), nil, nil
			}
			if calls == 2 {
				return nil, nil, nil
			}
			return []byte(`{"name":"space","state":"Stopping"}`), nil, nil
		},
	}
	_, err := runtime.StopCodespace(context.Background(), "space")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StopCodespace() error = %v, want timeout", err)
	}
}

func TestCodespaceRuntime_CommandErrors(t *testing.T) {
	originalPath := ghPath
	ghPath = func() (string, error) { return "/test/gh", nil }
	defer func() { ghPath = originalPath }()
	tests := []struct {
		name      string
		operation func(context.Context, codespaceRuntime) (CodespaceStatus, error)
		first     string
		failCall  int
	}{
		{
			name: "status",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.CodespaceStatus(ctx, "space")
			},
			failCall: 1,
		},
		{
			name: "start",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StartCodespace(ctx, "space")
			},
			first:    `{"name":"space","state":"Shutdown"}`,
			failCall: 2,
		},
		{
			name: "stop",
			operation: func(ctx context.Context, runtime codespaceRuntime) (CodespaceStatus, error) {
				return runtime.StopCodespace(ctx, "space")
			},
			first:    `{"name":"space","state":"Available"}`,
			failCall: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			runtime := codespaceRuntime{
				runCommand: func(context.Context, string, ...string) ([]byte, []byte, error) {
					calls++
					if calls == test.failCall {
						return nil, []byte("command failed"), errors.New("exit status 1")
					}
					return []byte(test.first), nil, nil
				},
			}
			if _, err := test.operation(context.Background(), runtime); err == nil || !strings.Contains(err.Error(), "command failed") {
				t.Fatalf("operation error = %v", err)
			}
		})
	}
}

func TestDispatchProtocolRequest_RejectsInvalidCodespaceParams(t *testing.T) {
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	tests := []string{
		`{"id":"1","method":"codespaces.status","params":{}}`,
		`{"id":"2","method":"codespaces.start","params":{"codespace":"space","extra":true}}`,
		`{"id":"3","method":"codespaces.stop","params":{"codespace":1}}`,
	}
	for _, line := range tests {
		response := dispatchProtocolRequest(context.Background(), []byte(line), supervisor)
		if response.Error == nil || response.Error.Code != "invalid_params" {
			t.Fatalf("response = %+v, want invalid_params", response)
		}
	}
}

func TestSupervisor_LeaseConflictAndStopRelease(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	first := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	second := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	spec := AgentStart{AgentID: "agent-1", Codespace: "Space", WorkingDirectory: "/work/./repo"}
	if _, err := first.Start(context.Background(), spec); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	response := dispatchProtocolRequest(context.Background(), []byte(
		`{"id":"2","method":"agent.start","params":{"agent_id":"agent-2","codespace":"space","working_directory":"/work/repo"}}`,
	), second)
	if response.Error == nil || response.Error.Code != "lease_conflict" {
		t.Fatalf("lease conflict response = %+v", response)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var conflict struct {
		Error struct {
			Context struct {
				Owner leaseOwner `json:"owner"`
			} `json:"context"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded, &conflict); err != nil || conflict.Error.Context.Owner.Codespace != "Space" {
		t.Fatalf("lease conflict context = %s, %v", encoded, err)
	}
	if _, err := first.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("first Stop() error = %v", err)
	}
	started, err := second.Start(context.Background(), AgentStart{
		AgentID:          "agent-3",
		Codespace:        "space",
		WorkingDirectory: "/work/repo",
	})
	if err != nil || started.State != AgentIdle {
		t.Fatalf("Start() after release = %+v, %v", started, err)
	}
	if err := second.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	third := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	if _, err := third.Start(context.Background(), AgentStart{AgentID: "agent-4", Codespace: "space", WorkingDirectory: "/work/repo"}); err != nil {
		t.Fatalf("Start() after shutdown error = %v", err)
	}
	if err := third.Shutdown(context.Background()); err != nil {
		t.Fatalf("third Shutdown() error = %v", err)
	}
}

func TestSupervisor_StartFailureReleasesLease(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	failing := newSupervisor(&fakeRuntime{startErr: errors.New("start failed")}, leases)
	if _, err := failing.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err == nil {
		t.Fatal("Start() succeeded")
	}
	working := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	if _, err := working.Start(context.Background(), AgentStart{AgentID: "agent-2", Codespace: "space"}); err != nil {
		t.Fatalf("Start() after failed start error = %v", err)
	}
}

func TestSupervisor_ContextCancellationReleasesLease(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	supervisor := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	if _, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	cancel()
	eventually(t, func() bool {
		status, err := supervisor.Status(context.Background(), "agent-1")
		return err == nil && status.State == AgentStopped
	})
	next := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	if _, err := next.Start(context.Background(), AgentStart{AgentID: "agent-2", Codespace: "space"}); err != nil {
		t.Fatalf("Start() after cancellation error = %v", err)
	}
	if err := next.Shutdown(context.Background()); err != nil {
		t.Fatalf("next Shutdown() error = %v", err)
	}
}

func TestSupervisor_StopReleasesLeaseWhenEventsAreSaturated(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	session := newFakeSession()
	supervisor := newSupervisor(&fakeRuntime{session: session}, leases)
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	for len(supervisor.events) < cap(supervisor.events) {
		supervisor.events <- protocolEvent{Type: "event"}
	}
	session.events <- NormalizedEvent{Kind: "assistant.message", Text: "blocked"}
	eventually(t, func() bool { return len(session.events) == 0 })

	done := make(chan error, 1)
	go func() {
		_, err := supervisor.Stop(context.Background(), "agent-1")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not finish after event saturation")
	}
	next := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
	if _, err := next.Start(context.Background(), AgentStart{AgentID: "agent-2", Codespace: "space"}); err != nil {
		t.Fatalf("Start() after saturated stop error = %v", err)
	}
	if err := next.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_StopPreemptsBlockedStart(t *testing.T) {
	tests := []struct {
		name string
		stop func(*Supervisor) error
	}{
		{
			name: "agent stop",
			stop: func(supervisor *Supervisor) error {
				snapshot, err := supervisor.Stop(context.Background(), "agent-1")
				if snapshot.State != AgentStopped {
					return fmt.Errorf("Stop() state = %q", snapshot.State)
				}
				return err
			},
		},
		{
			name: "shutdown",
			stop: func(supervisor *Supervisor) error {
				return supervisor.Shutdown(context.Background())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leases := newLeaseManager(t.TempDir())
			runtime := &fakeRuntime{session: newFakeSession(), startEntered: make(chan struct{}), blockStart: true}
			supervisor := newSupervisor(runtime, leases)
			startDone := make(chan error, 1)
			go func() {
				_, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"})
				startDone <- err
			}()
			select {
			case <-runtime.startEntered:
			case <-time.After(time.Second):
				t.Fatal("runtime.Start() did not begin")
			}
			if err := test.stop(supervisor); err != nil {
				t.Fatalf("stop error = %v", err)
			}
			status, err := supervisor.Status(context.Background(), "agent-1")
			if err != nil || status.State != AgentStopped {
				t.Fatalf("Status() after stop = %+v, %v", status, err)
			}
			select {
			case <-startDone:
			case <-time.After(time.Second):
				t.Fatal("blocked Start() did not return")
			}
			replacement := newSupervisor(&fakeRuntime{session: newFakeSession()}, leases)
			if _, err := replacement.Start(context.Background(), AgentStart{AgentID: "agent-2", Codespace: "space"}); err != nil {
				t.Fatalf("replacement Start() error = %v", err)
			}
			if err := replacement.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSupervisor_RetainsLeaseUntilForceStopCompletes(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	allowForce := make(chan struct{})
	supervisor := newSupervisor(&pendingForceRuntime{
		closeTimeout: time.Millisecond,
		allowForce:   allowForce,
	}, leases)
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.Stop(context.Background(), "agent-1"); err == nil {
		t.Fatal("Stop() succeeded before force termination completed")
	}
	if _, err := leases.Acquire("space", ""); err == nil {
		t.Fatal("replacement acquired the lease before force termination completed")
	}
	close(allowForce)
	var replacement *agentLease
	eventually(t, func() bool {
		lease, err := leases.Acquire("space", "")
		if err != nil {
			return false
		}
		replacement = lease
		return true
	})
	if err := replacement.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_ShutdownClosesAgentsConcurrently(t *testing.T) {
	leases := newLeaseManager(t.TempDir())
	runtime := &blockingCloseRuntime{closeTimeout: 100 * time.Millisecond}
	supervisor := newSupervisor(runtime, leases)
	for _, spec := range []AgentStart{
		{AgentID: "agent-1", Codespace: "space-1"},
		{AgentID: "agent-2", Codespace: "space-2"},
	} {
		if _, err := supervisor.Start(context.Background(), spec); err != nil {
			t.Fatalf("Start(%q) error = %v", spec.AgentID, err)
		}
	}
	started := time.Now()
	err := supervisor.Shutdown(context.Background())
	if err == nil {
		t.Fatal("Shutdown() succeeded despite blocking closes")
	}
	if elapsed := time.Since(started); elapsed >= 180*time.Millisecond {
		t.Fatalf("Shutdown() took %s, want one close timeout", elapsed)
	}

	for _, slot := range []string{"space-1", "space-2"} {
		var replacement *agentLease
		eventually(t, func() bool {
			lease, err := leases.Acquire(slot, "")
			if err != nil {
				return false
			}
			replacement = lease
			return true
		})
		if err := replacement.Release(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSDKRuntimeSession_CloseBoundsDisconnect(t *testing.T) {
	forced := make(chan struct{})
	serverClosed := make(chan struct{})
	runtime := &sdkRuntimeSession{
		events:      newNormalizedEventSink(),
		unsubscribe: func() {},
		disconnect: func() error {
			<-forced
			return nil
		},
		stop: func() error { return nil },
		forceStop: func() {
			close(forced)
		},
		serverClose: func() {
			close(serverClosed)
		},
		closeTimeout: time.Millisecond,
		terminated:   make(chan struct{}),
	}
	done := make(chan error, 1)
	go func() {
		done <- runtime.Close()
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not bound Disconnect()")
	}
	select {
	case <-forced:
	case <-time.After(time.Second):
		t.Fatal("Close() did not force-stop the client")
	}
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("Close() did not close the server")
	}
}

func TestSDKRuntimeSession_ClosePreservesErrors(t *testing.T) {
	disconnectErr := errors.New("disconnect failed")
	stopErr := errors.New("stop failed")
	runtime := &sdkRuntimeSession{
		events:      newNormalizedEventSink(),
		unsubscribe: func() {},
		disconnect:  func() error { return disconnectErr },
		stop:        func() error { return stopErr },
		forceStop:   func() {},
		serverClose: func() {},
		terminated:  make(chan struct{}),
	}
	err := runtime.Close()
	if !errors.Is(err, disconnectErr) || !errors.Is(err, stopErr) {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestSupervisor_RejectsDuplicateAgentID(t *testing.T) {
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	ctx := context.Background()
	if _, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	_, err := supervisor.Start(ctx, AgentStart{AgentID: "agent-1", Codespace: "other"})
	var protocolErr *protocolErrorValue
	if !errors.As(err, &protocolErr) || protocolErr.code != "agent_exists" {
		t.Fatalf("duplicate Start() error = %v", err)
	}
}

func TestSupervisor_RedactsSecrets(t *testing.T) {
	secret := "github_pat_1234567890_secret"
	supervisor := newTestSupervisor(t, &fakeRuntime{startErr: errors.New("remote failed with token=" + secret)})
	snapshot, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"})
	if err == nil || strings.Contains(snapshot.SafeError, secret) || !strings.Contains(snapshot.SafeError, "[REDACTED]") {
		t.Fatalf("Start() = %+v, %v", snapshot, err)
	}
	for len(supervisor.events) > 0 {
		event, marshalErr := json.Marshal(<-supervisor.events)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(event), secret) {
			t.Fatalf("protocol event exposed secret: %s", event)
		}
	}
	response := dispatchProtocolRequest(context.Background(), []byte(`{"id":"1","method":"agent.start","params":{"agent_id":"agent-1","codespace":"space"}}`), supervisor)
	encoded, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("protocol response exposed secret: %s", encoded)
	}
	if got := redactSecrets("Bearer " + secret); strings.Contains(got, secret) {
		t.Fatalf("redacted event text exposed secret: %q", got)
	}
}

func TestServeAgentProtocol_OverPipes(t *testing.T) {
	session := newFakeSession()
	session.events <- NormalizedEvent{Kind: "assistant.message", Text: "ready"}
	reader, input := io.Pipe()
	var output bytes.Buffer
	var diagnostics bytes.Buffer
	supervisor := NewSupervisor(&fakeRuntime{
		codespaces: []Codespace{{Name: "space"}},
		session:    session,
	})
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, &output, &diagnostics, supervisor)
	}()

	for _, line := range []string{
		`{"id":"1","method":"codespaces.list","params":{}}`,
		`{"id":"2","method":"agent.start","params":{"agent_id":"agent-1","codespace":"space"}}`,
		`{"id":"3","method":"agent.send","params":{"agent_id":"agent-1","prompt":"hello"}}`,
		`{"id":"4","method":"agent.stop","params":{"agent_id":"agent-1"}}`,
		`{"id":"5","method":"shutdown","params":{}}`,
	} {
		if _, err := io.WriteString(input, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveAgentProtocol() error = %v", err)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q", diagnostics.String())
	}
	var responseIDs []string
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var message struct {
			Type  string `json:"type"`
			ID    string `json:"id"`
			Event struct {
				Kind string `json:"kind"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "response" {
			responseIDs = append(responseIDs, message.ID)
		}
	}
	sort.Strings(responseIDs)
	if strings.Join(responseIDs, ",") != "1,2,3,4,5" {
		t.Fatalf("response IDs = %v", responseIDs)
	}
}

func TestServeAgentProtocol_StopsWithIdleOpenInput(t *testing.T) {
	reader, input := io.Pipe()
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	supervisor := NewSupervisor(&fakeRuntime{session: newFakeSession()})
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(ctx, reader, io.Discard, io.Discard, supervisor)
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serveAgentProtocol() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serveAgentProtocol() did not exit after context cancellation")
	}
}

func TestServeAgentProtocol_WriterFailureClosesInput(t *testing.T) {
	reader, input := io.Pipe()
	defer input.Close()
	writeErr := errors.New("output failed")
	supervisor := NewSupervisor(&fakeRuntime{session: newFakeSession()})
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, errorWriter{err: writeErr}, io.Discard, supervisor)
	}()

	if _, err := io.WriteString(input, `{"id":"1","method":"codespaces.list","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, writeErr) {
			t.Fatalf("serveAgentProtocol() error = %v, want %v", err, writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("serveAgentProtocol() did not return after writer failure")
	}
	if _, err := io.WriteString(input, "more input\n"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("input write error = %v, want closed pipe", err)
	}
}

func TestServeAgentProtocol_RegistersStartBeforeBackToBackStop(t *testing.T) {
	reader, input := io.Pipe()
	session := newFakeSession()
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, &output, io.Discard, supervisor)
	}()

	for _, line := range []string{
		`{"id":"start","method":"agent.start","params":{"agent_id":"agent-1","codespace":"space"}}`,
		`{"id":"stop","method":"agent.stop","params":{"agent_id":"agent-1"}}`,
	} {
		if _, err := io.WriteString(input, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, func() bool {
		snapshot, err := supervisor.Status(context.Background(), "agent-1")
		return err == nil && snapshot.State == AgentStopped
	})
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveAgentProtocol() error = %v", err)
	}

	var stop protocolResponse
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var response protocolResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.Type == "response" && response.ID == "stop" {
			stop = response
		}
	}
	if stop.ID != "stop" || stop.Error != nil {
		t.Fatalf("stop response = %+v, want a successful stop", stop)
	}
}

func TestServeAgentProtocol_WriterFailureDuringTerminalDrainAbortsOutput(t *testing.T) {
	reader, input := io.Pipe()
	writeErr := errors.New("terminal event write failed")
	writer := newTerminalEventFailureWriter(writeErr)
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, writer, io.Discard, supervisor)
	}()

	for _, line := range []string{
		`{"id":"start","method":"agent.start","params":{"agent_id":"agent-1","codespace":"space"}}`,
		`{"id":"shutdown","method":"shutdown","params":{}}`,
	} {
		if _, err := io.WriteString(input, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-writer.terminalSeen:
	case <-time.After(time.Second):
		t.Fatal("writer did not receive a terminal event")
	}
	select {
	case err := <-done:
		if !errors.Is(err, writeErr) {
			t.Fatalf("serveAgentProtocol() error = %v, want %v", err, writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal writer failure did not unblock shutdown")
	}
	select {
	case <-writer.closed:
	default:
		t.Fatal("terminal writer failure did not abort output")
	}
}

func TestServeAgentProtocol_ShutdownRespondsWithCleanupFailure(t *testing.T) {
	session := newFakeSession()
	closeErr := errors.New("session cleanup failed")
	session.closeErr = closeErr
	supervisor := newTestSupervisor(t, &fakeRuntime{session: session})
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	reader, input := io.Pipe()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, &output, io.Discard, supervisor)
	}()
	if _, err := io.WriteString(input, `{"id":"shutdown","method":"shutdown","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}

	if err := <-done; !errors.Is(err, closeErr) {
		t.Fatalf("serveAgentProtocol() error = %v, want %v", err, closeErr)
	}
	var response protocolResponse
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var candidate protocolResponse
		if err := json.Unmarshal([]byte(line), &candidate); err != nil {
			t.Fatal(err)
		}
		if candidate.ID == "shutdown" {
			response = candidate
		}
	}
	if response.Error == nil || response.Error.Code != "shutdown_failed" {
		t.Fatalf("shutdown response = %+v, want shutdown_failed", response)
	}
}

func TestServeAgentProtocol_InvalidShutdownContinuesServing(t *testing.T) {
	runtime := &fakeRuntime{
		codespaces: []Codespace{{Name: "space"}},
		session:    newFakeSession(),
	}
	supervisor := newTestSupervisor(t, runtime)
	input := strings.NewReader(strings.Join([]string{
		`{"id":"bad","method":"shutdown","params":{"unexpected":true}}`,
		`{"id":"list","method":"codespaces.list","params":{}}`,
		`{"id":"done","method":"shutdown","params":{}}`,
	}, "\n") + "\n")
	var output bytes.Buffer

	if err := serveAgentProtocol(context.Background(), input, &output, io.Discard, supervisor); err != nil {
		t.Fatalf("serveAgentProtocol() error = %v", err)
	}

	responses := make(map[string]protocolResponse)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var response protocolResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.Type == "response" {
			responses[response.ID] = response
		}
	}
	if response := responses["bad"]; response.Error == nil || response.Error.Code != "invalid_params" {
		t.Fatalf("invalid shutdown response = %+v", response)
	}
	if response := responses["list"]; response.Error != nil || response.Result == nil {
		t.Fatalf("list response after invalid shutdown = %+v", response)
	}
	if response := responses["done"]; response.Error != nil {
		t.Fatalf("valid shutdown response = %+v", response)
	}
}

func TestServeAgentProtocol_BoundsConcurrentDispatch(t *testing.T) {
	runtime := &blockingListRuntime{}
	supervisor := newTestSupervisor(t, runtime)
	var input strings.Builder
	for index := 0; index < protocolDispatchLimit+1; index++ {
		fmt.Fprintf(&input, `{"id":"list-%d","method":"codespaces.list","params":{}}`+"\n", index)
	}
	input.WriteString(`{"id":"shutdown","method":"shutdown","params":{}}` + "\n")
	var output bytes.Buffer

	if err := serveAgentProtocol(context.Background(), strings.NewReader(input.String()), &output, io.Discard, supervisor); err != nil {
		t.Fatalf("serveAgentProtocol() error = %v", err)
	}

	busyResponses := 0
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var response protocolResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error != nil && response.Error.Code == "server_busy" {
			busyResponses++
		}
	}
	if busyResponses != 1 {
		t.Fatalf("server_busy responses = %d, want 1", busyResponses)
	}
	if maxActive := runtime.maxActive.Load(); maxActive > protocolDispatchLimit {
		t.Fatalf("maximum active dispatches = %d, want at most %d", maxActive, protocolDispatchLimit)
	}
}

func TestServeAgentProtocol_ShutdownCancelsBlockedCodespaceDispatch(t *testing.T) {
	reader, input := io.Pipe()
	runtime := &fakeRuntime{
		session:               newFakeSession(),
		codespaceStartEntered: make(chan struct{}),
		blockCodespaceStart:   true,
	}
	supervisor := newTestSupervisor(t, runtime)
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, &output, io.Discard, supervisor)
	}()

	if _, err := io.WriteString(input, `{"id":"start","method":"codespaces.start","params":{"codespace":"space"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	<-runtime.codespaceStartEntered
	if _, err := io.WriteString(input, `{"id":"shutdown","method":"shutdown","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAgentProtocol() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for a canceled Codespace command")
	}
	for _, id := range []string{"start", "shutdown"} {
		if !strings.Contains(output.String(), `"id":"`+id+`"`) {
			t.Fatalf("output lacks %s response: %s", id, output.String())
		}
	}
}

func TestServeAgentProtocol_CancellationAbortsBlockedDrainWriter(t *testing.T) {
	reader, input := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := newBlockingProtocolWriter()
	supervisor := newTestSupervisor(t, &fakeRuntime{session: newFakeSession()})
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(ctx, reader, writer, io.Discard, supervisor)
	}()

	if _, err := io.WriteString(input, `{"id":"list","method":"codespaces.list","params":{}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	<-writer.entered
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serveAgentProtocol() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not abort the blocked output drain")
	}
	select {
	case <-writer.closed:
	default:
		t.Fatal("cancellation did not close the blocked output")
	}
}

func TestServeAgentProtocol_ReturnsScannerFailure(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", 1024*1024+1))
	err := serveAgentProtocol(
		context.Background(),
		input,
		io.Discard,
		io.Discard,
		NewSupervisor(&fakeRuntime{session: newFakeSession()}),
	)
	if err == nil {
		t.Fatalf("serveAgentProtocol() error = %v, want scanner failure", err)
	}
}

func TestServeAgentProtocol_StopsBlockedStartAndDrainsTerminalEvents(t *testing.T) {
	reader, input := io.Pipe()
	runtime := &fakeRuntime{session: newFakeSession(), startEntered: make(chan struct{}), blockStart: true}
	supervisor := newTestSupervisor(t, runtime)
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serveAgentProtocol(context.Background(), reader, &output, io.Discard, supervisor)
	}()

	if _, err := io.WriteString(input, `{"id":"start","method":"agent.start","params":{"agent_id":"agent-1","codespace":"space"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	<-runtime.startEntered
	for _, line := range []string{
		`{"id":"stop","method":"agent.stop","params":{"agent_id":"agent-1"}}`,
		`{"id":"shutdown","method":"shutdown","params":{}}`,
	} {
		if _, err := io.WriteString(input, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveAgentProtocol() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop and shutdown did not preempt blocked startup")
	}

	responseIDs := map[string]bool{}
	sequences := map[string][]uint64{}
	sawTerminal := false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var message struct {
			Type     string `json:"type"`
			ID       string `json:"id"`
			AgentID  string `json:"agent_id"`
			Sequence uint64 `json:"sequence"`
			Event    struct {
				Snapshot *AgentSnapshot `json:"snapshot"`
			} `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatal(err)
		}
		if message.Type == "response" {
			responseIDs[message.ID] = true
		}
		if message.Type == "event" {
			sequences[message.AgentID] = append(sequences[message.AgentID], message.Sequence)
			if message.Event.Snapshot != nil &&
				(message.Event.Snapshot.State == AgentStopped || message.Event.Snapshot.State == AgentFailed) {
				sawTerminal = true
			}
		}
	}
	for _, id := range []string{"start", "stop", "shutdown"} {
		if !responseIDs[id] {
			t.Fatalf("output lacks %q response: %s", id, output.String())
		}
	}
	for agentID, sequence := range sequences {
		for index := 1; index < len(sequence); index++ {
			if sequence[index] != sequence[index-1]+1 {
				t.Fatalf("%s event sequences = %v, want contiguous", agentID, sequence)
			}
		}
	}
	if !sawTerminal {
		t.Fatalf("output lacks terminal agent event: %s", output.String())
	}
}

func TestSupervisor_ExternalRuntimeDeathCleansUpAndReleasesLease(t *testing.T) {
	session := newFakeSession()
	leases := newLeaseManager(t.TempDir())
	supervisor := newSupervisor(&fakeRuntime{session: session}, leases)
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	session.fail(errors.New("transport vanished"))

	eventually(t, func() bool {
		snapshot, err := supervisor.Status(context.Background(), "agent-1")
		return err == nil && snapshot.State == AgentFailed
	})
	if session.closeCount() != 1 {
		t.Fatalf("session close count = %d, want 1", session.closeCount())
	}
	lease, err := leases.Acquire("space", ".")
	if err != nil {
		t.Fatalf("replacement lease acquisition error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_StopCodespaceStopsMatchingAgents(t *testing.T) {
	session := newFakeSession()
	leases := newLeaseManager(t.TempDir())
	runtime := &fakeRuntime{
		session: session,
		stopped: CodespaceStatus{Name: "space", State: CodespaceShutdown},
	}
	supervisor := newSupervisor(runtime, leases)
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "SPACE"}); err != nil {
		t.Fatal(err)
	}
	status, err := supervisor.StopCodespace(context.Background(), "space")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != CodespaceShutdown || session.closeCount() != 1 {
		t.Fatalf("StopCodespace() = %+v, close count = %d", status, session.closeCount())
	}
	lease, err := leases.Acquire("space", ".")
	if err != nil {
		t.Fatalf("lease was not released: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_StopCodespaceStillStopsAfterAgentFailure(t *testing.T) {
	session := newFakeSession()
	runtime := &fakeRuntime{
		session: session,
		stopped: CodespaceStatus{Name: "space", State: CodespaceShutdown},
	}
	supervisor := newTestSupervisor(t, runtime)
	if _, err := supervisor.Start(context.Background(), AgentStart{AgentID: "agent-1", Codespace: "space"}); err != nil {
		t.Fatal(err)
	}
	session.fail(errors.New("runtime disconnected"))
	eventually(t, func() bool {
		snapshot, err := supervisor.Status(context.Background(), "agent-1")
		return err == nil && snapshot.State == AgentFailed
	})

	status, err := supervisor.StopCodespace(context.Background(), "space")
	if err != nil {
		t.Fatalf("StopCodespace() error = %v", err)
	}
	if status.State != CodespaceShutdown {
		t.Fatalf("StopCodespace() = %+v, want shutdown", status)
	}
}

func TestSDKRuntimeSession_DoneReportsUnexpectedPingFailure(t *testing.T) {
	runtime := &sdkRuntimeSession{
		session:      &copilot.Session{SessionID: "session-1"},
		events:       newNormalizedEventSink(),
		runtimeDone:  make(chan error, 1),
		ping:         func(context.Context) error { return errors.New("connection lost") },
		pingInterval: time.Millisecond,
		pingTimeout:  time.Second,
		monitorDone:  make(chan struct{}),
		disconnect:   func() error { return nil },
		stop:         func() error { return nil },
		forceStop:    func() {},
		serverClose:  func() {},
		terminated:   make(chan struct{}),
		closeTimeout: time.Second,
	}
	runtime.startMonitor()
	select {
	case err := <-runtime.Done():
		if err == nil || !strings.Contains(err.Error(), "connection lost") {
			t.Fatalf("Done() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime did not report ping failure")
	}
}

func TestSDKRuntimeSession_CloseStopsHealthMonitor(t *testing.T) {
	pingStarted := make(chan struct{})
	runtime := &sdkRuntimeSession{
		session:     &copilot.Session{SessionID: "session-1"},
		events:      newNormalizedEventSink(),
		runtimeDone: make(chan error, 1),
		ping: func(ctx context.Context) error {
			close(pingStarted)
			<-ctx.Done()
			return ctx.Err()
		},
		pingInterval: time.Millisecond,
		pingTimeout:  time.Second,
		monitorDone:  make(chan struct{}),
		disconnect:   func() error { return nil },
		stop:         func() error { return nil },
		forceStop:    func() {},
		serverClose:  func() {},
		terminated:   make(chan struct{}),
		closeTimeout: time.Second,
	}
	runtime.startMonitor()
	<-pingStarted
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runtime.monitorDone:
	case <-time.After(time.Second):
		t.Fatal("health monitor did not stop during close")
	}
}

func TestNormalizedEventSink_CloseConcurrentCallbacks(t *testing.T) {
	sink := newNormalizedEventSink()
	var callbacks sync.WaitGroup
	for range 32 {
		callbacks.Add(1)
		go func() {
			defer callbacks.Done()
			for range 64 {
				sink.Send(NormalizedEvent{Kind: "assistant.message"})
			}
		}()
	}

	closed := make(chan struct{})
	go func() {
		sink.Close()
		close(closed)
	}()
	callbacks.Wait()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("event sink did not stop its callbacks")
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
