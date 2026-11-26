// Package debugger runs a local Delve DAP session for the editor.
package debugger

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

type State string

const (
	Starting State = "Starting"
	Running  State = "Running"
	Paused   State = "Paused"
	Stopping State = "Stopping"
	Finished State = "Finished"
)

type Source struct {
	Path string `json:"path"`
}

type Frame struct {
	ID, Line, Column int
	Name             string
	Source           Source
}

type Variable struct {
	Name, Value, Type  string
	VariablesReference int
}

type Scope struct {
	Name               string
	VariablesReference int
	Expensive          bool
}

type Breakpoint struct {
	ID, Line int
	Verified bool
	Message  string
}

// Snapshot slices and maps are read-only, and replaced rather than mutated.
type Snapshot struct {
	State            State
	Detail, Output   string
	Thread           int
	Frames           []Frame
	Selected         int // index into Frames, or -1
	Scopes           []Scope
	Variables        map[int][]Variable
	Breakpoints      map[string]map[int]Breakpoint // source path -> requested line -> result
	Location, StopID uint64                        // Location changes on navigation, StopID only on a new stop
}

type Action string

const (
	Continue    Action = "continue"
	Pause       Action = "pause"
	Next        Action = "next"
	StepIn      Action = "stepIn"
	StepOut     Action = "stepOut"
	SelectFrame Action = "frame"
	Expand      Action = "expand"
)

type request struct {
	action Action
	id     int
	stopID uint64
}

type Session struct {
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	changes      chan struct{}
	requests     chan request
	breakChanged chan struct{}
	mu           sync.Mutex
	snap         Snapshot
	wanted       map[string][]int
	output       []byte
}

// Start launches Delve in dir. Mode is "debug" for main packages or "test".
// Breakpoints use absolute paths and one-based line numbers.
func Start(command, dir, mode string, breakpoints map[string][]int, args ...string) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{ctx: ctx, cancel: cancel, done: make(chan struct{}), changes: make(chan struct{}, 1), requests: make(chan request, 16), breakChanged: make(chan struct{}, 1), snap: Snapshot{State: Starting, Selected: -1}, wanted: make(map[string][]int)}
	for path, lines := range breakpoints {
		s.wanted[path] = slices.Clone(lines)
	}
	go s.run(command, dir, mode, slices.Clone(args))
	return s
}

// OfflineSession returns a session that carries a fixed snapshot without a
// Delve process: the headless verification and demos render it like a live
// paused session. Its context starts canceled, so requests are rejected
// (the snapshot never changes) and Stop is a no-op — the usual shutdown
// paths work unchanged.
func OfflineSession(snap Snapshot) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Session{
		ctx: ctx, cancel: cancel,
		done: make(chan struct{}), changes: make(chan struct{}, 1),
		requests: make(chan request, 16), breakChanged: make(chan struct{}, 1),
		snap: snap, wanted: make(map[string][]int),
	}
	go func() {
		<-ctx.Done()
		close(s.done)
	}()
	return s
}

func (s *Session) Done() <-chan struct{}    { return s.done }
func (s *Session) Changes() <-chan struct{} { return s.changes }

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.snap
	snap.Output = string(s.output)
	return snap
}

func (s *Session) signal() {
	select {
	case s.changes <- struct{}{}:
	default:
	}
}

func (s *Session) update(f func(*Snapshot)) {
	s.mu.Lock()
	f(&s.snap)
	s.mu.Unlock()
	s.signal()
}

func (s *Session) addOutput(text string) {
	const limit = 64 << 10
	s.mu.Lock()
	if len(text) >= limit {
		s.output = append(s.output[:0], text[len(text)-limit:]...)
	} else {
		if drop := len(s.output) + len(text) - limit; drop > 0 {
			s.output = s.output[:copy(s.output, s.output[drop:])]
		}
		s.output = append(s.output, text...)
	}
	s.mu.Unlock()
	s.signal()
}

// AppendOutput feeds the session's output buffer directly (the debug
// panel's Output tab renders it). Live processes stream Delve's
// stdout/stderr through the same buffer; demo and headless-verification
// sessions inject synthetic log lines through this method.
func (s *Session) AppendOutput(text string) { s.addOutput(text) }

func (s *Session) Stop() { s.cancel() }

func (s *Session) SetBreakpoints(path string, lines []int) {
	s.mu.Lock()
	if len(lines) == 0 {
		delete(s.wanted, path)
	} else {
		s.wanted[path] = slices.Clone(lines)
	}
	s.mu.Unlock()
	select {
	case s.breakChanged <- struct{}{}:
	default:
	}
}

func (s *Session) Request(action Action, id int, stopID uint64) bool {
	if s.ctx.Err() != nil {
		return false
	}
	select {
	case s.requests <- request{action, id, stopID}:
		return true
	default:
		return false
	}
}

func (s *Session) run(command, dir, mode string, args []string) {
	var proc *delveProcess
	var p *protocol
	var out string
	detail := "Program finished"
	defer func() {
		defer close(s.done)
		defer s.cancel()
		if s.ctx.Err() != nil {
			detail = "Session stopped"
		}
		s.update(func(snap *Snapshot) { snap.State = Stopping; clearStack(snap) })
		if p != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = p.call(ctx, "disconnect", map[string]any{"terminateDebuggee": true}, nil)
			cancel()
			_ = p.conn.Close()
			<-p.done
		}
		proc.close(p != nil)
		if out != "" {
			_ = os.RemoveAll(out)
		}
		s.update(func(snap *Snapshot) { snap.State, snap.Detail = Finished, detail })
	}()
	var err error
	out, err = os.MkdirTemp("", "go-gpui-debug-")
	if err != nil {
		detail = err.Error()
		return
	}
	var conn net.Conn
	proc, conn, err = s.connect(dir, command)
	if err != nil {
		detail = err.Error()
		return
	}
	p = newProtocol(conn, s.addOutput)
	bin := filepath.Join(out, "debug-app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if err := s.serve(p, dir, mode, bin, args); err != nil {
		detail = err.Error()
	}
}

func (s *Session) serve(p *protocol, dir, mode, bin string, args []string) error {
	ctx := s.ctx
	if err := p.call(ctx, "initialize", map[string]any{"adapterID": "go", "clientID": "go-gpui", "linesStartAt1": true, "columnsStartAt1": true, "pathFormat": "path", "supportsVariableType": true}, nil); err != nil {
		return err
	}
	launchArgs := map[string]any{"mode": mode, "program": dir, "cwd": dir, "output": bin, "buildFlags": "-mod=readonly"}
	if len(args) > 0 {
		launchArgs["args"] = args
	}
	id, launch, err := p.send("launch", launchArgs)
	defer p.forget(id)
	if err != nil {
		return err
	}
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	installed := make(map[string][]int)
	// DAP permits the launch response either before or after configuration.
	configured := false
	for !configured || launch != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("Delve disconnected during launch: %w", p.err)
		case <-timer.C:
			return fmt.Errorf("Delve did not finish launching")
		case msg := <-launch:
			if err := response("launch", msg, nil); err != nil {
				return err
			}
			launch = nil
		case msg := <-p.events:
			switch msg.Event {
			case "initialized":
				s.syncBreakpoints(p, installed)
				if err := p.call(ctx, "configurationDone", nil, nil); err != nil {
					return err
				}
				s.update(func(snap *Snapshot) { snap.State = Running })
				configured = true
			default:
				if done, err := s.event(p, msg); done {
					return err
				}
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.done:
			return fmt.Errorf("Delve disconnected: %w", p.err)
		case <-s.breakChanged:
			s.syncBreakpoints(p, installed)
		case cmd := <-s.requests:
			s.handle(p, installed, cmd)
		case msg := <-p.events:
			if done, err := s.event(p, msg); done {
				return err
			}
		}
	}
}

func (s *Session) syncBreakpoints(p *protocol, installed map[string][]int) {
	s.mu.Lock()
	wanted := copyMap(s.wanted)
	s.mu.Unlock()
	for path := range installed {
		if _, ok := wanted[path]; !ok {
			wanted[path] = nil
		}
	}
	for path, lines := range wanted {
		if slices.Equal(lines, installed[path]) {
			continue
		}
		points := make([]map[string]int, 0, len(lines))
		for _, line := range lines {
			points = append(points, map[string]int{"line": line})
		}
		var reply struct{ Breakpoints []Breakpoint }
		err := p.call(s.ctx, "setBreakpoints", map[string]any{"source": Source{Path: path}, "breakpoints": points}, &reply)
		if err == nil {
			if len(lines) == 0 {
				delete(installed, path)
			} else {
				installed[path] = lines
			}
		} else {
			s.addOutput(err.Error() + "\n")
		}
		results := make(map[int]Breakpoint, len(lines))
		for i, line := range lines {
			switch {
			case err != nil:
				results[line] = Breakpoint{Message: err.Error()}
			case i < len(reply.Breakpoints):
				results[line] = reply.Breakpoints[i]
			default:
				results[line] = Breakpoint{Message: "No breakpoint returned by Delve"}
			}
		}
		s.update(func(snap *Snapshot) {
			b := copyMap(snap.Breakpoints)
			if len(results) == 0 {
				delete(b, path)
			} else {
				b[path] = results
			}
			snap.Breakpoints = b
		})
	}
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	n := make(map[K]V, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func clearStack(snap *Snapshot) {
	snap.Frames, snap.Scopes, snap.Variables, snap.Selected = nil, nil, nil, -1
}

func (s *Session) event(p *protocol, msg message) (bool, error) {
	switch msg.Event {
	case "terminated":
		return true, nil
	case "exited":
		var body struct{ ExitCode int }
		_ = json.Unmarshal(msg.Body, &body)
		if body.ExitCode != 0 {
			return true, fmt.Errorf("Program exited with code %d", body.ExitCode)
		}
		return true, nil
	case "continued":
		s.update(func(snap *Snapshot) { snap.State, snap.Detail = Running, ""; clearStack(snap) })
	case "stopped":
		var body struct {
			ThreadID                  int
			Reason, Description, Text string
		}
		if err := json.Unmarshal(msg.Body, &body); err != nil {
			return true, err
		}
		detail := strings.TrimSpace(strings.Join([]string{body.Reason, body.Description, body.Text}, " "))
		var stack struct{ StackFrames []Frame }
		err := p.call(s.ctx, "stackTrace", map[string]int{"threadId": body.ThreadID, "levels": 50}, &stack)
		if err != nil {
			s.addOutput(err.Error() + "\n")
		}
		s.update(func(snap *Snapshot) {
			clearStack(snap)
			snap.StopID++
			snap.State, snap.Thread, snap.Detail, snap.Frames = Paused, body.ThreadID, detail, stack.StackFrames
		})
		if len(stack.StackFrames) != 0 {
			s.selectFrame(p, 0, stack.StackFrames[0].ID)
		}
	case "breakpoint":
		var body struct{ Breakpoint Breakpoint }
		if json.Unmarshal(msg.Body, &body) != nil || body.Breakpoint.ID == 0 {
			break
		}
		s.update(func(snap *Snapshot) {
			for path, results := range snap.Breakpoints {
				for line, bp := range results {
					if bp.ID == body.Breakpoint.ID {
						all, file := copyMap(snap.Breakpoints), copyMap(results)
						file[line], all[path] = body.Breakpoint, file
						snap.Breakpoints = all
						return
					}
				}
			}
		})
	}
	return false, nil
}

func (s *Session) selectFrame(p *protocol, index, frameID int) {
	s.update(func(snap *Snapshot) { snap.Selected = index; snap.Location++; snap.Scopes, snap.Variables = nil, nil })
	var result struct{ Scopes []Scope }
	if err := p.call(s.ctx, "scopes", map[string]int{"frameId": frameID}, &result); err != nil {
		s.addOutput(err.Error() + "\n")
		return
	}
	s.update(func(snap *Snapshot) { snap.Scopes = result.Scopes })
	for _, scope := range result.Scopes {
		if !scope.Expensive && scope.VariablesReference > 0 {
			s.expand(p, scope.VariablesReference)
		}
	}
}

func (s *Session) expand(p *protocol, ref int) {
	var result struct{ Variables []Variable }
	if err := p.call(s.ctx, "variables", map[string]int{"variablesReference": ref}, &result); err != nil {
		s.addOutput(err.Error() + "\n")
		return
	}
	s.update(func(snap *Snapshot) {
		vars := copyMap(snap.Variables)
		vars[ref] = result.Variables
		snap.Variables = vars
	})
}

func (s *Session) handle(p *protocol, installed map[string][]int, cmd request) {
	s.mu.Lock()
	snap := s.snap
	s.mu.Unlock()
	if cmd.stopID != snap.StopID {
		return
	}
	switch cmd.action {
	case Pause:
		if snap.State != Running {
			return
		}
	case Continue, Next, StepIn, StepOut:
		if snap.State != Paused {
			return
		}
		s.syncBreakpoints(p, installed)
	case SelectFrame:
		if snap.State == Paused && cmd.id >= 0 && cmd.id < len(snap.Frames) {
			s.selectFrame(p, cmd.id, snap.Frames[cmd.id].ID)
		}
		return
	case Expand:
		if _, loaded := snap.Variables[cmd.id]; snap.State == Paused && cmd.id > 0 && !loaded {
			s.expand(p, cmd.id)
		}
		return
	default:
		return
	}
	if err := p.call(s.ctx, string(cmd.action), map[string]int{"threadId": snap.Thread}, nil); err != nil {
		s.addOutput(err.Error() + "\n")
	} else if cmd.action != Pause {
		s.update(func(snap *Snapshot) { snap.State, snap.Detail = Running, ""; clearStack(snap) })
	}
}
