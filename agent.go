package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type AgentState string

const (
	AgentStarting AgentState = "starting"
	AgentIdle     AgentState = "idle"
	AgentRunning  AgentState = "running"
	AgentStopping AgentState = "stopping"
	AgentStopped  AgentState = "stopped"
	AgentFailed   AgentState = "failed"
)

type AgentSnapshot struct {
	AgentID   string     `json:"agent_id"`
	Codespace string     `json:"codespace"`
	State     AgentState `json:"state"`
	SessionID string     `json:"session_id,omitempty"`
	Sequence  uint64     `json:"sequence"`
	SafeError string     `json:"safe_error,omitempty"`
}

type AgentStart struct {
	AgentID               string `json:"agent_id"`
	Codespace             string `json:"codespace"`
	WorkingDirectory      string `json:"working_directory,omitempty"`
	ApproveAllPermissions bool   `json:"approve_all_permissions"`
}

type NormalizedEvent struct {
	Kind      string
	Text      string
	SafeError string
}

type RuntimeSession interface {
	ID() string
	Send(context.Context, string) error
	Events() <-chan NormalizedEvent
	Done() <-chan error
	Close() error
}

type terminationAwareSession interface {
	TerminationDone() <-chan struct{}
}

type RuntimeFactory interface {
	ListCodespaces(context.Context) ([]Codespace, error)
	CodespaceStatus(context.Context, string) (CodespaceStatus, error)
	StartCodespace(context.Context, string) (CodespaceStatus, error)
	StopCodespace(context.Context, string) (CodespaceStatus, error)
	Start(context.Context, AgentStart) (RuntimeSession, error)
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Context any    `json:"context,omitempty"`
}

type protocolResponse struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Result any            `json:"result,omitempty"`
	Error  *protocolError `json:"error,omitempty"`
}

type protocolEvent struct {
	Type     string      `json:"type"`
	AgentID  string      `json:"agent_id"`
	Sequence uint64      `json:"sequence"`
	Event    stableEvent `json:"event"`
}

type stableEvent struct {
	Kind      string         `json:"kind"`
	Text      string         `json:"text,omitempty"`
	SafeError string         `json:"safe_error,omitempty"`
	Snapshot  *AgentSnapshot `json:"snapshot,omitempty"`
}

type protocolRequest struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type agentCommand struct {
	kind   string
	prompt string
	reply  chan agentResult
}

type agentResult struct {
	snapshot AgentSnapshot
	err      error
}

type managedAgent struct {
	spec     AgentStart
	runtime  RuntimeFactory
	leases   *leaseManager
	ctx      context.Context
	cancel   context.CancelFunc
	stopping chan struct{}
	stopOnce sync.Once
	commands chan agentCommand
	done     chan struct{}
	final    AgentSnapshot
	finalErr error
	events   chan<- protocolEvent
}

type Supervisor struct {
	runtime RuntimeFactory
	leases  *leaseManager

	mu        sync.Mutex
	agents    map[string]*managedAgent
	events    chan protocolEvent
	closed    bool
	closeOnce sync.Once
}

func NewSupervisor(runtime RuntimeFactory) *Supervisor {
	return newSupervisor(runtime, defaultLeaseManager())
}

func newSupervisor(runtime RuntimeFactory, leases *leaseManager) *Supervisor {
	return &Supervisor{
		runtime: runtime,
		leases:  leases,
		agents:  make(map[string]*managedAgent),
		events:  make(chan protocolEvent, 256),
	}
}

// Events returns asynchronous protocol events in each agent's sequence order.
func (s *Supervisor) Events() <-chan protocolEvent {
	return s.events
}

// ListCodespaces lists Codespaces without a selection prompt.
func (s *Supervisor) ListCodespaces(ctx context.Context) ([]Codespace, error) {
	return s.runtime.ListCodespaces(ctx)
}

func (s *Supervisor) CodespaceStatus(ctx context.Context, codespace string) (CodespaceStatus, error) {
	return s.runtime.CodespaceStatus(ctx, codespace)
}

func (s *Supervisor) StartCodespace(ctx context.Context, codespace string) (CodespaceStatus, error) {
	return s.runtime.StartCodespace(ctx, codespace)
}

func (s *Supervisor) StopCodespace(ctx context.Context, codespace string) (CodespaceStatus, error) {
	s.mu.Lock()
	agents := make([]*managedAgent, 0)
	for _, agent := range s.agents {
		if strings.EqualFold(strings.TrimSpace(agent.spec.Codespace), strings.TrimSpace(codespace)) {
			agents = append(agents, agent)
		}
	}
	s.mu.Unlock()
	activeAgents := agents[:0]
	for _, agent := range agents {
		select {
		case <-agent.done:
		default:
			activeAgents = append(activeAgents, agent)
		}
	}
	agentErr := stopManagedAgents(activeAgents)
	status, codespaceErr := s.runtime.StopCodespace(ctx, codespace)
	return status, errors.Join(agentErr, codespaceErr)
}

func (s *Supervisor) Start(ctx context.Context, spec AgentStart) (AgentSnapshot, error) {
	agent, err := s.registerAgent(ctx, spec)
	if err != nil {
		return AgentSnapshot{}, err
	}
	return s.startRegisteredAgent(ctx, agent)
}

func (s *Supervisor) registerAgent(ctx context.Context, spec AgentStart) (*managedAgent, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, protocolFailure("supervisor_stopped", "the supervisor has stopped")
	}
	if _, exists := s.agents[spec.AgentID]; exists {
		s.mu.Unlock()
		return nil, protocolFailure("agent_exists", "an agent with this agent_id already exists")
	}

	agentCtx, cancel := context.WithCancel(ctx)
	agent := &managedAgent{
		spec:     spec,
		runtime:  s.runtime,
		leases:   s.leases,
		ctx:      agentCtx,
		cancel:   cancel,
		stopping: make(chan struct{}),
		commands: make(chan agentCommand),
		done:     make(chan struct{}),
		events:   s.events,
		final: AgentSnapshot{
			AgentID:   spec.AgentID,
			Codespace: spec.Codespace,
			State:     AgentStarting,
		},
	}
	s.agents[spec.AgentID] = agent
	s.mu.Unlock()

	go agent.run()
	return agent, nil
}

func (s *Supervisor) startRegisteredAgent(ctx context.Context, agent *managedAgent) (AgentSnapshot, error) {
	return agent.ask(ctx, agentCommand{kind: "start"})
}

// Send accepts a prompt for an idle agent.
func (s *Supervisor) Send(ctx context.Context, agentID, prompt string) (AgentSnapshot, error) {
	agent, err := s.agent(agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}

	return agent.ask(ctx, agentCommand{kind: "send", prompt: prompt})
}

func (s *Supervisor) Status(ctx context.Context, agentID string) (AgentSnapshot, error) {
	agent, err := s.agent(agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}

	return agent.ask(ctx, agentCommand{kind: "status"})
}

func (s *Supervisor) Stop(ctx context.Context, agentID string) (AgentSnapshot, error) {
	agent, err := s.agent(agentID)
	if err != nil {
		return AgentSnapshot{}, err
	}

	agent.requestStop()
	return agent.ask(ctx, agentCommand{kind: "stop"})
}

// Shutdown stops every registered agent and closes the event stream.
func (s *Supervisor) Shutdown(_ context.Context) error {
	var shutdownErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		agents := make([]*managedAgent, 0, len(s.agents))
		for _, agent := range s.agents {
			agents = append(agents, agent)
		}
		s.mu.Unlock()

		shutdownErr = stopManagedAgents(agents)
		close(s.events)
	})
	return shutdownErr
}

func stopManagedAgents(agents []*managedAgent) error {
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].spec.AgentID < agents[j].spec.AgentID
	})
	for _, agent := range agents {
		agent.requestStop()
	}
	type stopResult struct {
		index int
		err   error
	}
	results := make(chan stopResult, len(agents))
	for index, agent := range agents {
		go func(index int, agent *managedAgent) {
			_, err := agent.ask(context.Background(), agentCommand{kind: "stop"})
			<-agent.done
			results <- stopResult{index: index, err: err}
		}(index, agent)
	}
	stopErrors := make([]error, len(agents))
	for range agents {
		result := <-results
		stopErrors[result.index] = result.err
	}
	var stopErr error
	for _, err := range stopErrors {
		stopErr = errors.Join(stopErr, err)
	}
	return stopErr
}

func (s *Supervisor) agent(agentID string) (*managedAgent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	agent, exists := s.agents[agentID]
	if !exists {
		return nil, protocolFailure("agent_not_found", "no agent exists with this agent_id")
	}
	return agent, nil
}

func (a *managedAgent) ask(ctx context.Context, command agentCommand) (AgentSnapshot, error) {
	command.reply = make(chan agentResult, 1)
	select {
	case <-a.done:
		return a.resultAfterDone(command)
	case <-ctx.Done():
		return AgentSnapshot{}, ctx.Err()
	case a.commands <- command:
	}

	select {
	case <-a.done:
		return a.resultAfterDone(command)
	case <-ctx.Done():
		return AgentSnapshot{}, ctx.Err()
	case result := <-command.reply:
		return result.snapshot, result.err
	}
}

func (a *managedAgent) resultAfterDone(command agentCommand) (AgentSnapshot, error) {
	if command.kind == "send" {
		return a.final, protocolFailure("agent_stopped", "the agent has stopped")
	}
	if command.kind == "stop" {
		return a.final, a.finalErr
	}
	return a.final, nil
}

func (a *managedAgent) requestStop() {
	a.stopOnce.Do(func() {
		close(a.stopping)
		a.cancel()
	})
}

func (a *managedAgent) run() {
	snapshot := a.final
	var session RuntimeSession
	var lease *agentLease
	var runtimeEvents <-chan NormalizedEvent
	var runtimeDone <-chan error
	var sends <-chan error
	releaseLease := func() error {
		if lease == nil {
			return nil
		}
		err := lease.Release()
		lease = nil
		return err
	}

	emit := func(event protocolEvent) {
		select {
		case a.events <- event:
			return
		default:
		}
		select {
		case a.events <- event:
		case <-a.ctx.Done():
		case <-a.stopping:
		}
	}
	emitSnapshot := func() {
		snapshot.Sequence++
		copy := snapshot
		emit(protocolEvent{
			Type:     "event",
			AgentID:  snapshot.AgentID,
			Sequence: snapshot.Sequence,
			Event:    stableEvent{Kind: "agent.state", Snapshot: &copy},
		})
	}
	emitRuntimeEvent := func(event NormalizedEvent) {
		snapshot.Sequence++
		emit(protocolEvent{
			Type:     "event",
			AgentID:  snapshot.AgentID,
			Sequence: snapshot.Sequence,
			Event: stableEvent{
				Kind:      event.Kind,
				Text:      redactSecrets(event.Text),
				SafeError: redactSecrets(event.SafeError),
			},
		})
	}
	finish := func(cause error) error {
		snapshot.State = AgentStopping
		emitSnapshot()
		a.cancel()
		err := cause
		if session != nil {
			err = errors.Join(err, session.Close())
		}
		if terminating, ok := session.(terminationAwareSession); ok {
			select {
			case <-terminating.TerminationDone():
				err = errors.Join(err, releaseLease())
			default:
				go func() {
					<-terminating.TerminationDone()
					_ = releaseLease()
				}()
			}
		} else {
			err = errors.Join(err, releaseLease())
		}
		if err != nil {
			snapshot.State = AgentFailed
			snapshot.SafeError = redactSecrets(err.Error())
			emitSnapshot()
			return err
		}
		snapshot.State = AgentStopped
		snapshot.SafeError = ""
		emitSnapshot()
		return err
	}

	for {
		select {
		case command := <-a.commands:
			switch command.kind {
			case "start":
				emitSnapshot()
				acquired, err := a.leases.Acquire(a.spec.Codespace, a.spec.WorkingDirectory)
				if err != nil {
					snapshot.State = AgentFailed
					snapshot.SafeError = redactSecrets(err.Error())
					emitSnapshot()
					var conflict *leaseConflictError
					if errors.As(err, &conflict) {
						command.reply <- agentResult{
							snapshot: snapshot,
							err: protocolFailureWithContext("lease_conflict", snapshot.SafeError, struct {
								Owner *leaseOwner `json:"owner"`
							}{Owner: conflict.Owner}),
						}
					} else {
						command.reply <- agentResult{snapshot: snapshot, err: protocolFailure("agent_start_failed", snapshot.SafeError)}
					}
					continue
				}
				lease = acquired
				started, err := a.runtime.Start(a.ctx, a.spec)
				if err != nil {
					err = errors.Join(err, releaseLease())
					snapshot.State = AgentFailed
					snapshot.SafeError = redactSecrets(err.Error())
					emitSnapshot()
					command.reply <- agentResult{snapshot: snapshot, err: protocolFailure("agent_start_failed", snapshot.SafeError)}
					continue
				}
				session = started
				runtimeEvents = session.Events()
				runtimeDone = session.Done()
				snapshot.SessionID = session.ID()
				snapshot.State = AgentIdle
				emitSnapshot()
				command.reply <- agentResult{snapshot: snapshot}
			case "status":
				command.reply <- agentResult{snapshot: snapshot}
			case "send":
				if snapshot.State != AgentIdle {
					command.reply <- agentResult{snapshot: snapshot, err: protocolFailure("agent_not_idle", "the agent is not ready to accept a prompt")}
					continue
				}
				snapshot.State = AgentRunning
				emitSnapshot()
				done := make(chan error, 1)
				go func() {
					done <- session.Send(a.ctx, command.prompt)
				}()
				sends = done
				command.reply <- agentResult{snapshot: snapshot}
			case "stop":
				err := finish(nil)
				command.reply <- agentResult{snapshot: snapshot, err: err}
				a.final = snapshot
				a.finalErr = err
				close(a.done)
				return
			}
		case err := <-sends:
			sends = nil
			if err != nil {
				snapshot.State = AgentFailed
				snapshot.SafeError = redactSecrets(err.Error())
				emitSnapshot()
			}
		case event, ok := <-runtimeEvents:
			if !ok {
				runtimeEvents = nil
				continue
			}
			emitRuntimeEvent(event)
			switch event.Kind {
			case "assistant.idle":
				if snapshot.State == AgentRunning {
					snapshot.State = AgentIdle
					emitSnapshot()
				}
			case "session.error":
				snapshot.State = AgentFailed
				snapshot.SafeError = redactSecrets(event.SafeError)
				emitSnapshot()
			}
		case err, ok := <-runtimeDone:
			if !ok {
				err = errors.New("runtime transport stopped")
			}
			a.finalErr = finish(err)
			a.final = snapshot
			close(a.done)
			return
		case <-a.ctx.Done():
			a.finalErr = finish(nil)
			a.final = snapshot
			close(a.done)
			return
		}
	}
}

func protocolFailure(code, message string) error {
	return &protocolErrorValue{code: code, message: message}
}

func protocolFailureWithContext(code, message string, context any) error {
	return &protocolErrorValue{code: code, message: message, context: context}
}

type protocolErrorValue struct {
	code    string
	message string
	context any
}

func (e *protocolErrorValue) Error() string {
	return e.message
}

func errorForProtocol(err error) protocolError {
	var protocolErr *protocolErrorValue
	if errors.As(err, &protocolErr) {
		return protocolError{Code: protocolErr.code, Message: redactSecrets(protocolErr.message), Context: protocolErr.context}
	}
	return protocolError{Code: "internal_error", Message: redactSecrets(err.Error())}
}

type protocolInput struct {
	line []byte
	err  error
}

const protocolDispatchLimit = 32

func scanProtocolInput(input io.Reader, stop <-chan struct{}) <-chan protocolInput {
	inputs := make(chan protocolInput, 1)
	go func() {
		defer close(inputs)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			message := protocolInput{line: append([]byte(nil), scanner.Bytes()...)}
			select {
			case inputs <- message:
			case <-stop:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case inputs <- protocolInput{err: err}:
			case <-stop:
			}
		}
	}()
	return inputs
}

func closeProtocolStream(stream any) {
	if closer, ok := stream.(io.Closer); ok {
		_ = closer.Close()
	}
}

func serveAgentProtocol(ctx context.Context, input io.Reader, output io.Writer, diagnostics io.Writer, supervisor *Supervisor) error {
	dispatchCtx, cancelDispatch := context.WithCancel(ctx)
	defer cancelDispatch()
	serveDone := make(chan struct{})
	defer close(serveDone)
	messages := make(chan any)
	writerDone := make(chan error, 1)
	stopInput := make(chan struct{})
	abortOutput := make(chan struct{})
	eventsDone := make(chan struct{})
	var stopInputOnce sync.Once
	var abortOutputOnce sync.Once
	stopReading := func() {
		stopInputOnce.Do(func() {
			close(stopInput)
			closeProtocolStream(input)
		})
	}
	stopWriting := func() {
		abortOutputOnce.Do(func() {
			close(abortOutput)
			closeProtocolStream(output)
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			stopWriting()
		case <-serveDone:
		}
	}()
	send := func(message any) bool {
		select {
		case messages <- message:
			return true
		case <-abortOutput:
			return false
		}
	}
	go func() {
		encoder := json.NewEncoder(output)
		for message := range messages {
			if err := encoder.Encode(message); err != nil {
				stopWriting()
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()
	go func() {
		defer close(eventsDone)
		for event := range supervisor.Events() {
			select {
			case <-abortOutput:
				continue
			default:
			}
			if !send(event) {
				continue
			}
		}
	}()

	inputs := scanProtocolInput(input, stopInput)
	var dispatches sync.WaitGroup
	dispatchSlots := make(chan struct{}, protocolDispatchLimit)
	var serveErr error
	writerFinished := false
	var shutdownID string
	reserveDispatch := func() bool {
		select {
		case dispatchSlots <- struct{}{}:
			return true
		default:
			return false
		}
	}
	dispatch := func(run func() protocolResponse) {
		dispatches.Add(1)
		go func() {
			defer dispatches.Done()
			defer func() { <-dispatchSlots }()
			send(run())
		}()
	}
	for {
		select {
		case input, ok := <-inputs:
			if !ok {
				goto shutdown
			}
			if input.err != nil {
				fmt.Fprintf(diagnostics, "agent serve input error: %s\n", redactSecrets(input.err.Error()))
				serveErr = input.err
				goto shutdown
			}
			request, requestErr := parseProtocolRequest(input.line)
			if requestErr == nil && request.Method == "shutdown" {
				var params struct{}
				if err := decodeStrict(request.Params, &params); err == nil {
					shutdownID = request.ID
					goto shutdown
				}
			}
			if !reserveDispatch() {
				send(protocolResponseForError(request.ID, protocolFailure("server_busy", "the supervisor has too many requests in flight")))
				continue
			}
			if prepared, ok := prepareAgentStart(dispatchCtx, request, requestErr, supervisor); ok {
				dispatch(prepared)
				continue
			}
			line := input.line
			dispatch(func() protocolResponse {
				return dispatchProtocolRequest(dispatchCtx, line, supervisor)
			})
		case err := <-writerDone:
			serveErr = err
			writerFinished = true
			goto shutdown
		case <-ctx.Done():
			serveErr = ctx.Err()
			goto shutdown
		}
	}

shutdown:
	cancelDispatch()
	stopReading()
	if writerFinished || errors.Is(serveErr, context.Canceled) {
		stopWriting()
	}
	shutdownErr := supervisor.Shutdown(context.Background())
	if shutdownErr != nil {
		if serveErr == nil {
			serveErr = shutdownErr
		}
		fmt.Fprintf(diagnostics, "agent serve shutdown error: %s\n", redactSecrets(shutdownErr.Error()))
	}
	dispatches.Wait()
	if shutdownID != "" {
		if shutdownErr != nil {
			send(protocolResponseForError(shutdownID, protocolFailure("shutdown_failed", redactSecrets(shutdownErr.Error()))))
		} else {
			send(protocolResponse{Type: "response", ID: shutdownID, Result: struct {
				Stopped bool `json:"stopped"`
			}{Stopped: true}})
		}
	}
	<-eventsDone
	close(messages)
	if err := ctx.Err(); err != nil {
		stopWriting()
		return err
	}
	if !writerFinished {
		if errors.Is(serveErr, context.Canceled) {
			return serveErr
		}
		if err := <-writerDone; err != nil && serveErr == nil {
			serveErr = err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return serveErr
}

func prepareAgentStart(ctx context.Context, request protocolRequest, requestErr error, supervisor *Supervisor) (func() protocolResponse, bool) {
	if requestErr != nil || request.Method != "agent.start" {
		return nil, false
	}
	var params AgentStart
	if err := decodeStrict(request.Params, &params); err != nil || params.AgentID == "" || params.Codespace == "" {
		return nil, false
	}
	agent, err := supervisor.registerAgent(ctx, params)
	return func() protocolResponse {
		if err != nil {
			return protocolResponseForError(request.ID, err)
		}
		snapshot, err := supervisor.startRegisteredAgent(ctx, agent)
		if err != nil {
			return protocolResponseForError(request.ID, err)
		}
		return protocolResponse{Type: "response", ID: request.ID, Result: snapshot}
	}, true
}

func protocolResponseForError(id string, err error) protocolResponse {
	protocolErr := errorForProtocol(err)
	return protocolResponse{Type: "response", ID: id, Error: &protocolErr}
}

func dispatchProtocolRequest(ctx context.Context, line []byte, supervisor *Supervisor) protocolResponse {
	request, err := parseProtocolRequest(line)
	if err != nil {
		return protocolResponse{Type: "response", Error: &protocolError{Code: "invalid_request", Message: err.Error()}}
	}

	result, err := dispatchCommand(ctx, request, supervisor)
	if err != nil {
		return protocolResponseForError(request.ID, err)
	}
	return protocolResponse{Type: "response", ID: request.ID, Result: result}
}

func parseProtocolRequest(line []byte) (protocolRequest, error) {
	var request protocolRequest
	if err := decodeStrict(line, &request); err != nil {
		return protocolRequest{}, fmt.Errorf("request must be one JSON object")
	}
	if request.ID == "" {
		return protocolRequest{}, fmt.Errorf("request id must be a non-empty string")
	}
	if request.Method == "" {
		return protocolRequest{}, fmt.Errorf("request method must be a non-empty string")
	}
	if len(request.Params) == 0 {
		return protocolRequest{}, fmt.Errorf("request params must be an object")
	}
	if !bytes.HasPrefix(bytes.TrimSpace(request.Params), []byte("{")) {
		return protocolRequest{}, fmt.Errorf("request params must be an object")
	}
	return request, nil
}

func dispatchCommand(ctx context.Context, request protocolRequest, supervisor *Supervisor) (any, error) {
	switch request.Method {
	case "codespaces.list":
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return nil, protocolFailure("invalid_params", "codespaces.list params must be an empty object")
		}
		return supervisor.ListCodespaces(ctx)
	case "codespaces.status":
		var params struct {
			Codespace string `json:"codespace"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.Codespace == "" {
			return nil, protocolFailure("invalid_params", "codespaces.status requires codespace")
		}
		status, err := supervisor.CodespaceStatus(ctx, params.Codespace)
		if err != nil {
			return nil, protocolFailure("codespace_status_failed", redactSecrets(err.Error()))
		}
		return status, nil
	case "codespaces.start":
		var params struct {
			Codespace string `json:"codespace"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.Codespace == "" {
			return nil, protocolFailure("invalid_params", "codespaces.start requires codespace")
		}
		status, err := supervisor.StartCodespace(ctx, params.Codespace)
		if err != nil {
			return nil, protocolFailure("codespace_start_failed", redactSecrets(err.Error()))
		}
		return status, nil
	case "codespaces.stop":
		var params struct {
			Codespace string `json:"codespace"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.Codespace == "" {
			return nil, protocolFailure("invalid_params", "codespaces.stop requires codespace")
		}
		status, err := supervisor.StopCodespace(ctx, params.Codespace)
		if err != nil {
			return nil, protocolFailure("codespace_stop_failed", redactSecrets(err.Error()))
		}
		return status, nil
	case "agent.start":
		var params AgentStart
		if err := decodeStrict(request.Params, &params); err != nil || params.AgentID == "" || params.Codespace == "" {
			return nil, protocolFailure("invalid_params", "agent.start requires agent_id and codespace")
		}
		return supervisor.Start(ctx, params)
	case "agent.send":
		var params struct {
			AgentID string `json:"agent_id"`
			Prompt  string `json:"prompt"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.AgentID == "" || params.Prompt == "" {
			return nil, protocolFailure("invalid_params", "agent.send requires agent_id and prompt")
		}
		snapshot, err := supervisor.Send(ctx, params.AgentID, params.Prompt)
		if err != nil {
			return nil, err
		}
		return struct {
			Accepted bool          `json:"accepted"`
			Agent    AgentSnapshot `json:"agent"`
		}{Accepted: true, Agent: snapshot}, nil
	case "agent.status":
		var params struct {
			AgentID string `json:"agent_id"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.AgentID == "" {
			return nil, protocolFailure("invalid_params", "agent.status requires agent_id")
		}
		return supervisor.Status(ctx, params.AgentID)
	case "agent.stop":
		var params struct {
			AgentID string `json:"agent_id"`
		}
		if err := decodeStrict(request.Params, &params); err != nil || params.AgentID == "" {
			return nil, protocolFailure("invalid_params", "agent.stop requires agent_id")
		}
		return supervisor.Stop(ctx, params.AgentID)
	case "shutdown":
		var params struct{}
		if err := decodeStrict(request.Params, &params); err != nil {
			return nil, protocolFailure("invalid_params", "shutdown params must be an empty object")
		}
		return struct {
			Stopped bool `json:"stopped"`
		}{Stopped: true}, supervisor.Shutdown(ctx)
	default:
		return nil, protocolFailure("method_not_found", "unknown method")
	}
}

func decodeStrict(data []byte, target any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return errors.New("value must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("value must contain one JSON object")
		}
		return err
	}
	return nil
}

var secretPattern = regexp.MustCompile(`(?i)(github_pat_[a-z0-9_]+|gh[pousr]_[a-z0-9_]+|(?:bearer|token|secret|password)\s*[=: ]\s*)[^\s,;]+`)

func redactSecrets(value string) string {
	return secretPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := strings.SplitN(match, "=", 2)
		if len(parts) == 2 {
			return parts[0] + "=[REDACTED]"
		}
		if strings.HasPrefix(strings.ToLower(match), "bearer ") {
			return "Bearer [REDACTED]"
		}
		if strings.HasPrefix(strings.ToLower(match), "token ") {
			return "token [REDACTED]"
		}
		if strings.HasPrefix(strings.ToLower(match), "secret ") {
			return "secret [REDACTED]"
		}
		if strings.HasPrefix(strings.ToLower(match), "password ") {
			return "password [REDACTED]"
		}
		return "[REDACTED]"
	})
}

type codespaceCommandRunner func(context.Context, string, ...string) ([]byte, []byte, error)

// CodespaceState is the stable state reported by the agent protocol.
type CodespaceState string

const (
	CodespaceAvailable  CodespaceState = "available"
	CodespaceStarting   CodespaceState = "starting"
	CodespaceStopping   CodespaceState = "stopping"
	CodespaceRebuilding CodespaceState = "rebuilding"
	CodespaceShutdown   CodespaceState = "shutdown"
	CodespaceUnknown    CodespaceState = "unknown"
)

// CodespaceStatus is the stable protocol projection of a Codespace.
type CodespaceStatus struct {
	Name  string         `json:"name"`
	State CodespaceState `json:"state"`
}

type codespaceRuntime struct {
	runCommand   codespaceCommandRunner
	pollInterval time.Duration
	pollTimeout  time.Duration
}

func runCodespaceCommand(ctx context.Context, executable string, args ...string) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func (r codespaceRuntime) command(ctx context.Context, executable string, args ...string) ([]byte, []byte, error) {
	runner := r.runCommand
	if runner == nil {
		runner = runCodespaceCommand
	}
	return runner(ctx, executable, args...)
}

func (r codespaceRuntime) ListCodespaces(ctx context.Context) ([]Codespace, error) {
	executable, err := ghPath()
	if err != nil {
		return nil, err
	}
	args := []string{"codespace", "list", "--json", "name,displayName,repository,gitStatus,state,lastUsedAt"}
	stdout, stderr, err := r.command(ctx, executable, args...)
	if err != nil {
		return nil, fmt.Errorf("error listing codespaces: %w\nStderr: %s", err, stderr)
	}
	var codespaces []Codespace
	if err := json.Unmarshal(stdout, &codespaces); err != nil {
		return nil, fmt.Errorf("error parsing codespace list: %w", err)
	}
	return codespaces, nil
}

func (r codespaceRuntime) CodespaceStatus(ctx context.Context, name string) (CodespaceStatus, error) {
	executable, err := ghPath()
	if err != nil {
		return CodespaceStatus{}, err
	}
	stdout, stderr, err := r.command(ctx, executable, "codespace", "view", "--codespace", name, "--json", "name,state")
	if err != nil {
		return CodespaceStatus{}, fmt.Errorf("view codespace: %w: %s", err, redactSecrets(string(stderr)))
	}
	return parseCodespaceStatus(stdout)
}

func (r codespaceRuntime) StartCodespace(ctx context.Context, name string) (CodespaceStatus, error) {
	status, err := r.CodespaceStatus(ctx, name)
	if err != nil {
		return CodespaceStatus{}, err
	}
	if status.State == CodespaceAvailable {
		return status, nil
	}
	if status.State == CodespaceStarting || status.State == CodespaceRebuilding {
		return r.waitForCodespaceAvailable(ctx, name)
	}
	if status.State == CodespaceStopping {
		status, err = r.waitForStartOutcome(ctx, name)
		if err != nil {
			return CodespaceStatus{}, err
		}
		if status.State == CodespaceAvailable {
			return status, nil
		}
	}
	executable, err := ghPath()
	if err != nil {
		return CodespaceStatus{}, err
	}
	_, stderr, err := r.command(ctx, executable, "api", "-X", "POST", "/user/codespaces/"+name+"/start")
	if err != nil {
		return CodespaceStatus{}, fmt.Errorf("start codespace: %w: %s", err, redactSecrets(string(stderr)))
	}
	return r.waitForCodespaceAvailable(ctx, name)
}

func (r codespaceRuntime) StopCodespace(ctx context.Context, name string) (CodespaceStatus, error) {
	status, err := r.CodespaceStatus(ctx, name)
	if err != nil {
		return CodespaceStatus{}, err
	}
	if status.State == CodespaceShutdown {
		return status, nil
	}
	if status.State != CodespaceStopping {
		executable, err := ghPath()
		if err != nil {
			return CodespaceStatus{}, err
		}
		_, stderr, err := r.command(ctx, executable, "codespace", "stop", "--codespace", name)
		if err != nil {
			return CodespaceStatus{}, fmt.Errorf("stop codespace: %w: %s", err, redactSecrets(string(stderr)))
		}
	}
	return r.waitForCodespaceState(ctx, name, CodespaceShutdown)
}

func (r codespaceRuntime) waitForCodespaceAvailable(ctx context.Context, name string) (CodespaceStatus, error) {
	return r.waitForCodespaceState(ctx, name, CodespaceAvailable)
}

func (r codespaceRuntime) waitForStartOutcome(ctx context.Context, name string) (CodespaceStatus, error) {
	return r.waitForCodespace(ctx, name, func(status CodespaceStatus) bool {
		return status.State == CodespaceAvailable || status.State == CodespaceShutdown
	})
}

func (r codespaceRuntime) waitForCodespaceState(ctx context.Context, name string, target CodespaceState) (CodespaceStatus, error) {
	return r.waitForCodespace(ctx, name, func(status CodespaceStatus) bool {
		return status.State == target
	})
}

func (r codespaceRuntime) waitForCodespace(ctx context.Context, name string, done func(CodespaceStatus) bool) (CodespaceStatus, error) {
	timeout := r.pollTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	pollInterval := r.pollInterval
	if pollInterval == 0 {
		pollInterval = time.Second
	}
	pollContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		status, err := r.CodespaceStatus(pollContext, name)
		if err != nil {
			return CodespaceStatus{}, err
		}
		if done(status) {
			return status, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-pollContext.Done():
			timer.Stop()
			return CodespaceStatus{}, pollContext.Err()
		case <-timer.C:
		}
	}
}

func parseCodespaceStatus(data []byte) (CodespaceStatus, error) {
	var response struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := decodeStrict(data, &response); err != nil {
		return CodespaceStatus{}, fmt.Errorf("parse codespace status: %w", err)
	}
	if response.Name == "" {
		return CodespaceStatus{}, errors.New("parse codespace status: missing name")
	}
	return CodespaceStatus{Name: response.Name, State: mapCodespaceState(response.State)}, nil
}

func mapCodespaceState(state string) CodespaceState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "available":
		return CodespaceAvailable
	case "starting", "pending":
		return CodespaceStarting
	case "stopping", "shuttingdown":
		return CodespaceStopping
	case "rebuilding":
		return CodespaceRebuilding
	case "shutdown":
		return CodespaceShutdown
	default:
		return CodespaceUnknown
	}
}

func (codespaceRuntime) Start(ctx context.Context, spec AgentStart) (RuntimeSession, error) {
	server, err := setupAgentServer(ctx, spec.AgentID)
	if err != nil {
		return nil, err
	}
	if err := prepareCodespaceScripts(ctx, spec.Codespace, false, false); err != nil {
		server.Close()
		return nil, err
	}
	ghPath, err := ghPath()
	if err != nil {
		server.Close()
		return nil, err
	}
	client := copilot.NewClient(&copilot.ClientOptions{
		Connection: copilot.StdioConnection{
			Path: ghPath,
			Args: remoteCopilotArgs(spec.Codespace, server),
		},
		LogLevel: "error",
	})
	if err := client.Start(ctx); err != nil {
		server.Close()
		return nil, err
	}
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		ClientName:          "gh-ado-codespaces",
		WorkingDirectory:    spec.WorkingDirectory,
		OnPermissionRequest: agentPermissionHandler(spec.ApproveAllPermissions),
	})
	if err != nil {
		_ = client.Stop()
		server.Close()
		return nil, err
	}
	return newSDKRuntimeSession(client, session, server), nil
}

var ghPath = func() (string, error) {
	return gh.Path()
}

func remoteCopilotArgs(codespace string, server *ServerConfig) []string {
	forward := fmt.Sprintf("%s:%s:%d", server.SocketPath, localServiceHost, server.Port)
	authSocket := "GH_ADO_CODESPACES_AUTH_SOCKET=" + server.SocketPath
	return []string{"codespace", "ssh", "--codespace", codespace, "--", "-T", "-R", forward, "env", authSocket, "copilot"}
}

type sdkRuntimeSession struct {
	session       *copilot.Session
	events        *normalizedEventSink
	unsubscribe   func()
	disconnect    func() error
	stop          func() error
	forceStop     func()
	serverClose   func()
	closeTimeout  time.Duration
	terminated    chan struct{}
	terminateOnce sync.Once
	runtimeDone   chan error
	doneOnce      sync.Once
	ping          func(context.Context) error
	pingInterval  time.Duration
	pingTimeout   time.Duration
	monitorCancel context.CancelFunc
	monitorDone   chan struct{}
	monitorOnce   sync.Once
	closeOnce     sync.Once
	closeErr      error
}

func newSDKRuntimeSession(client *copilot.Client, session *copilot.Session, server *ServerConfig) *sdkRuntimeSession {
	runtime := &sdkRuntimeSession{
		session:     session,
		events:      newNormalizedEventSink(),
		disconnect:  session.Disconnect,
		stop:        client.Stop,
		forceStop:   client.ForceStop,
		serverClose: server.Close,
		terminated:  make(chan struct{}),
		runtimeDone: make(chan error, 1),
		ping: func(ctx context.Context) error {
			_, err := client.Ping(ctx, "gh-ado-codespaces health check")
			return err
		},
		pingInterval: 5 * time.Second,
		pingTimeout:  5 * time.Second,
		monitorDone:  make(chan struct{}),
	}
	runtime.unsubscribe = session.On(func(event copilot.SessionEvent) {
		runtime.events.Send(stableSDKEvent(event))
	})
	runtime.startMonitor()
	return runtime
}

func (s *sdkRuntimeSession) ID() string {
	return s.session.SessionID
}

func (s *sdkRuntimeSession) Send(ctx context.Context, prompt string) error {
	_, err := s.session.Send(ctx, copilot.MessageOptions{Prompt: prompt})
	return err
}

func (s *sdkRuntimeSession) Events() <-chan NormalizedEvent {
	return s.events.Events()
}

func (s *sdkRuntimeSession) Done() <-chan error {
	return s.runtimeDone
}

func (s *sdkRuntimeSession) TerminationDone() <-chan struct{} {
	return s.terminated
}

func (s *sdkRuntimeSession) markTerminated() {
	s.terminateOnce.Do(func() {
		close(s.terminated)
	})
}

func (s *sdkRuntimeSession) startMonitor() {
	if s.ping == nil || s.runtimeDone == nil {
		return
	}
	s.monitorOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.monitorCancel = cancel
		go func() {
			defer close(s.monitorDone)
			interval := s.pingInterval
			if interval == 0 {
				interval = 5 * time.Second
			}
			timer := time.NewTimer(interval)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				timeout := s.pingTimeout
				if timeout == 0 {
					timeout = 5 * time.Second
				}
				pingCtx, cancelPing := context.WithTimeout(ctx, timeout)
				err := s.ping(pingCtx)
				cancelPing()
				if err != nil {
					select {
					case <-ctx.Done():
						return
					default:
						s.reportDone(fmt.Errorf("runtime transport stopped: %w", err))
						return
					}
				}
				timer.Reset(interval)
			}
		}()
	})
}

func (s *sdkRuntimeSession) stopMonitor() {
	if s.monitorCancel != nil {
		s.monitorCancel()
	}
}

func (s *sdkRuntimeSession) reportDone(err error) {
	if s.runtimeDone == nil {
		return
	}
	s.doneOnce.Do(func() {
		if err != nil {
			s.runtimeDone <- err
		}
		close(s.runtimeDone)
	})
}

func (s *sdkRuntimeSession) Close() error {
	s.closeOnce.Do(func() {
		s.stopMonitor()
		s.events.Close()
		if s.unsubscribe != nil {
			s.unsubscribe()
		}
		timeout := s.closeTimeout
		if timeout == 0 {
			timeout = 10 * time.Second
		}
		done := make(chan error, 1)
		go func() {
			done <- errors.Join(s.disconnect(), s.stop())
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case s.closeErr = <-done:
			s.markTerminated()
		case <-timer.C:
			s.closeErr = errors.New("sdk runtime cleanup timed out")
			go func() {
				s.forceStop()
				s.markTerminated()
			}()
		}
		if s.serverClose != nil {
			s.serverClose()
		}
		s.reportDone(nil)
	})
	return s.closeErr
}

func stableSDKEvent(event copilot.SessionEvent) NormalizedEvent {
	switch data := event.Data.(type) {
	case *copilot.AssistantMessageData:
		return NormalizedEvent{Kind: "assistant.message", Text: data.Content}
	case *copilot.AssistantMessageDeltaData:
		return NormalizedEvent{Kind: "assistant.message_delta", Text: data.DeltaContent}
	case *copilot.SessionIdleData:
		return NormalizedEvent{Kind: "assistant.idle"}
	case *copilot.SessionErrorData:
		return NormalizedEvent{Kind: "session.error", SafeError: data.Message}
	default:
		return NormalizedEvent{Kind: string(event.Type())}
	}
}

type normalizedEventSink struct {
	events    chan NormalizedEvent
	stopped   chan struct{}
	mu        sync.Mutex
	callbacks sync.WaitGroup
	closing   bool
	closeOnce sync.Once
}

func newNormalizedEventSink() *normalizedEventSink {
	return &normalizedEventSink{
		events:  make(chan NormalizedEvent, 256),
		stopped: make(chan struct{}),
	}
}

func (s *normalizedEventSink) Events() <-chan NormalizedEvent {
	return s.events
}

func (s *normalizedEventSink) Send(event NormalizedEvent) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.callbacks.Add(1)
	s.mu.Unlock()
	defer s.callbacks.Done()

	select {
	case s.events <- event:
	case <-s.stopped:
	}
}

func (s *normalizedEventSink) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		close(s.stopped)
		s.mu.Unlock()
		s.callbacks.Wait()
	})
}

func agentPermissionHandler(approveAll bool) copilot.PermissionHandlerFunc {
	return func(request copilot.PermissionRequest, invocation copilot.PermissionInvocation) (rpc.PermissionDecision, error) {
		if !approveAll || invocation.ManagedSettingsEnabled {
			return &rpc.PermissionDecisionUserNotAvailable{}, nil
		}
		if request.RequiresManagedApproval() {
			return &rpc.PermissionDecisionReject{}, nil
		}
		return &rpc.PermissionDecisionApproveOnce{}, nil
	}
}

func runAgentServe(ctx context.Context) error {
	supervisor := NewSupervisor(codespaceRuntime{})
	return serveAgentProtocol(ctx, os.Stdin, os.Stdout, os.Stderr, supervisor)
}
