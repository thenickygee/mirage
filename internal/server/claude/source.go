package claude

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/thenickygee/mirage/internal/server"
)

// Type aliases for the shared session model so the Claude source references the
// same types the Pool exposes, identical to the opencode client.
type (
	TrackedSession = server.TrackedSession
	Permission     = server.Permission
	OutputLine     = server.OutputLine
)

// Config holds the launch defaults for Claude sessions, populated from main.go
// flags. Zero values mean "let claude pick its default".
type Config struct {
	Bare           bool     // run claude with --bare (API-key auth, no hooks/CLAUDE.md/plugins)
	APIKey         string   // explicit ANTHROPIC_API_KEY; with Bare it falls back to $ANTHROPIC_API_KEY
	Model          string   // optional --model override
	PermissionMode string   // acceptEdits | auto | dontAsk | plan | default | ""
	AllowedTools   []string // optional --allowedTools list; anything else prompts via the UI
	WorkingDir     string   // cwd for launched subprocesses
	InitialPrompt  string   // optional: launch a session at startup (Phase 2 smoke test)
}

// Source implements server.Source for Claude Code sessions. Each session is a
// separate `claude -p … --output-format stream-json` subprocess whose NDJSON
// stdout is parsed by parse.go into TrackedSession state and whose stdin
// answers tool permission prompts (permissions.go). The source
// stays "connected" as long as the `claude` binary is on $PATH; per-session
// subprocess death is tracked on the session, not the source.
type Source struct {
	cfg     Config
	onEvent func()

	mu            sync.Mutex
	sessions      map[string]*TrackedSession // sessionID -> state
	busySessions  map[string]bool            // sessionID -> busy
	sessionAgents map[string]string          // sessionID -> agent name (always "" for claude)
	toolUseNames  map[string]string          // toolUseID -> tool name
	outputDirty   map[string]bool            // sessionID -> true when new output since last fetch
	streamingIdx  map[string]int             // sessionID -> in-progress streaming text line index (-1 via absence)
	pending       map[string]*Permission     // permissionID -> permission awaiting a UI answer
	pendingReqs   map[string]*pendingRequest // permissionID -> context needed to answer it
	queued        map[string][]string        // sessionID -> follow-ups waiting for the running turn
	procs         map[string]*sessionProc    // sessionID -> running subprocess
	pendingID     string                     // pre-generated UUID awaiting system/init confirmation

	connected      bool
	disconnectedAt *time.Time
	isNewInstance  bool
	closed         bool

	ctx               context.Context
	cancel            context.CancelFunc
	done              chan struct{}
	discDone          chan struct{} // closed when the discoverer goroutine exits
	discovererStarted bool

	// external tracks sessions discovered on disk (started outside Mirage).
	// Owned by the discoverer goroutine; entries here are also present in
	// s.sessions so the overview merges them transparently.
	external map[string]*externalTail
}

// NewSource constructs a Claude Code source. Call AddSource on the pool to
// register and Connect it.
func NewSource(cfg Config, onEvent func()) *Source {
	ctx, cancel := context.WithCancel(context.Background())
	return &Source{
		cfg:           cfg,
		onEvent:       onEvent,
		sessions:      make(map[string]*TrackedSession),
		busySessions:  make(map[string]bool),
		sessionAgents: make(map[string]string),
		toolUseNames:  make(map[string]string),
		outputDirty:   make(map[string]bool),
		streamingIdx:  make(map[string]int),
		pending:       make(map[string]*Permission),
		pendingReqs:   make(map[string]*pendingRequest),
		queued:        make(map[string][]string),
		procs:         make(map[string]*sessionProc),
		external:      make(map[string]*externalTail),
		isNewInstance: true,
		ctx:           ctx,
		cancel:        cancel,
		done:          make(chan struct{}),
		discDone:      make(chan struct{}),
	}
}

// ID returns the source's stable identifier. A single Claude source manages all
// claude subprocesses, so it uses the constant "claude".
func (s *Source) ID() string { return "claude" }

// Kind reports the source kind for UI badges and routing.
func (s *Source) Kind() string { return "claude" }

// Connect verifies the `claude` binary is on $PATH and marks the source
// connected. There is no health endpoint. If Config.InitialPrompt is set, a
// session is launched immediately (Phase 2 smoke-test entry point).
func (s *Source) Connect() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found on $PATH: %w", err)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("claude source is closed")
	}
	s.connected = true
	s.disconnectedAt = nil
	prompt := s.cfg.InitialPrompt
	s.mu.Unlock()

	if prompt != "" {
		// Launch in a goroutine so Connect stays non-blocking: AddSource
		// runs during main() setup before the Bubble Tea program is
		// running, and a synchronous onEvent here would deadlock p.Send.
		go s.launchSession(prompt, "", "")
	}
	// Start the external-session discoverer so Claude sessions launched
	// outside Mirage (e.g. an interactive `claude` TUI in another terminal)
	// show up in the overview and update live.
	s.discovererStarted = true
	s.startDiscoverer()
	return nil
}

func (s *Source) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

func (s *Source) DisconnectedAt() *time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disconnectedAt
}

func (s *Source) IsNewInstance() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isNewInstance
}

// launchSession spawns a `claude -p` subprocess for the given prompt. A fresh
// session is started when resumeID is empty; a follow-up turn is sent via
// `--resume <resumeID>` otherwise. For fresh sessions a pre-generated UUID is
// passed via --session-id so the UI can select the session before
// system/init arrives. The stdout stream is scanned and dispatched to
// parse.go in a goroutine. workingDir overrides the source's configured
// WorkingDir for this launch when non-empty (used by the UI launcher).
func (s *Source) launchSession(prompt, resumeID, workingDir string) {
	key := s.cfg.APIKey
	if key == "" && s.cfg.Bare {
		key = os.Getenv("ANTHROPIC_API_KEY")
	}
	if workingDir == "" {
		workingDir = s.cfg.WorkingDir
	}

	// For resume, reuse the existing session ID and pass --resume (not
	// --session-id). For fresh sessions, pre-generate a UUID.
	var sessionID, sidFlag string
	if resumeID != "" {
		sessionID = resumeID
	} else {
		sidFlag = newUUID()
		sessionID = sidFlag
	}

	res, err := launch(s.ctx, launchConfig{
		Prompt:         prompt,
		ResumeID:       resumeID,
		SessionID:      sidFlag,
		Model:          s.cfg.Model,
		PermissionMode: s.cfg.PermissionMode,
		AllowedTools:   s.cfg.AllowedTools,
		Bare:           s.cfg.Bare,
		APIKey:         key,
		WorkingDir:     workingDir,
	})
	if err != nil {
		s.recordLaunchFailure(err, sessionID, resumeID, workingDir)
		return
	}
	proc := res.proc

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		proc.cancel()
		_ = proc.cmd.Wait()
		return
	}
	if resumeID != "" {
		// Mark the existing session busy again and echo the user's message
		// into the transcript so the follow-up is visible.
		ts, ok := s.sessions[resumeID]
		if !ok {
			// Defensive: shouldn't happen — SendMessage checks ownership.
			ts = &TrackedSession{ID: resumeID, Title: "Claude Session"}
			s.sessions[resumeID] = ts
		}
		if strings.TrimSpace(prompt) != "" {
			ts.OutputLines = append(ts.OutputLines, OutputLine{
				Role: "user",
				Text: prompt,
			})
		}
		ts.Busy = true
		ts.ToolState = ""
		ts.ToolName = ""
		ts.WaitingForInput = false
		ts.LastActiveAt = time.Now()
	} else {
		// Fresh session: pre-register so it appears in the overview
		// immediately, marked busy until the result event lands.
		s.sessions[sessionID] = &TrackedSession{
			ID:           sessionID,
			Title:        "Claude Session",
			Directory:    workingDir,
			Busy:         true,
			LastActiveAt: time.Now(),
		}
		s.pendingID = sessionID
	}
	s.busySessions[sessionID] = true
	s.isNewInstance = false
	s.procs[sessionID] = proc
	s.mu.Unlock()
	s.fireEvent()

	// Drain stderr (diagnostics) in the background.
	go s.drainStderr(proc)
	// Scan stdout NDJSON and dispatch events.
	go s.scanStdout(sessionID, res)
}

// recordLaunchFailure surfaces a failed launch in the transcript so the user
// sees why nothing happened.
func (s *Source) recordLaunchFailure(err error, sessionID, resumeID, workingDir string) {
	line := OutputLine{Role: "assistant", Text: "✗ " + err.Error()}
	s.mu.Lock()
	if resumeID != "" {
		// Follow-up launch failed: surface the error on the existing
		// session rather than creating a phantom new one.
		delete(s.queued, resumeID)
		if ts, ok := s.sessions[resumeID]; ok {
			ts.OutputLines = append(ts.OutputLines, line)
			s.outputDirty[resumeID] = true
		}
	} else {
		s.sessions[sessionID] = &TrackedSession{
			ID:          sessionID,
			Title:       "Claude Session",
			Directory:   workingDir,
			OutputLines: []OutputLine{line},
		}
	}
	s.mu.Unlock()
	s.fireEvent()
}

// readLine returns the next non-empty NDJSON line. Unlike bufio.Scanner it has
// no line-length cap: a single stream-json event (a large file read, say) can
// exceed any fixed buffer, and giving up mid-stream would leave claude blocked
// on a full pipe.
func readLine(r *bufio.Reader) ([]byte, error) {
	for {
		line, err := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			return trimmed, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// scanStdout reads the claude subprocess's NDJSON stdout line-by-line and
// dispatches each line to handleLine. On EOF it finalizes the session if no
// result event was received (unexpected exit), then starts any queued
// follow-up turn.
func (s *Source) scanStdout(sessionID string, res *launchResult) {
	proc := res.proc
	defer close(proc.done)
	defer proc.cancel()
	reader := bufio.NewReader(res.stdout)
	sid := sessionID
	for {
		line, err := readLine(reader)
		if err != nil {
			break
		}
		select {
		case <-s.ctx.Done():
			proc.cancel()
			_ = proc.cmd.Wait()
			return
		default:
		}
		sid = s.handleLine(line, sid, proc)
	}

	// stdout closed: the process has exited. Give stderr a moment to drain so
	// the captured text is complete, then reap it.
	select {
	case <-proc.stderrDone:
	case <-time.After(2 * time.Second):
	}
	exitErr := proc.cmd.Wait()

	s.mu.Lock()
	ts, ok := s.sessions[sid]
	stillBusy := ok && s.busySessions[sid]
	if stillBusy {
		ts.Busy = false
		ts.WaitingForInput = false
		ts.ToolState = ""
		ts.LastActiveAt = time.Now()
		delete(s.busySessions, sid)
	}
	stderrText := strings.TrimSpace(proc.errBuf.String())
	if s.procs[sessionID] == proc {
		delete(s.procs, sessionID)
	}
	s.dropPendingLocked(proc)
	s.mu.Unlock()

	if stillBusy {
		s.recordUnexpectedExit(sid, stderrText, exitErr)
	}
	s.startQueued(sid)
}

// recordUnexpectedExit appends the reason claude exited without a result
// event. Stderr is preferred over exitErr, which is just "exit status 1".
func (s *Source) recordUnexpectedExit(sid, stderrText string, exitErr error) {
	msg := stderrText
	if msg == "" {
		msg = "claude process exited unexpectedly"
		if exitErr != nil {
			msg = exitErr.Error()
		}
	}
	s.mu.Lock()
	if ts, ok := s.sessions[sid]; ok {
		ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "assistant", Text: "✗ " + msg})
		s.outputDirty[sid] = true
	}
	s.mu.Unlock()
	s.fireEvent()
}

// startQueued launches the next follow-up that arrived while the previous turn
// was still running.
func (s *Source) startQueued(sid string) {
	s.mu.Lock()
	var next string
	if q := s.queued[sid]; len(q) > 0 && !s.closed {
		next = q[0]
		s.queued[sid] = q[1:]
	}
	s.mu.Unlock()
	if next != "" {
		s.launchSession(next, sid, "")
	}
}

// drainStderr copies the claude subprocess's stderr into the proc's errBuf for
// later surfacing on unexpected exit.
func (s *Source) drainStderr(proc *sessionProc) {
	defer close(proc.stderrDone)
	_, _ = io.Copy(proc.errBuf, proc.stderr)
}

// --- Sessions ---

func (s *Source) TrackedSessions() []*TrackedSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	pendingBySession := make(map[string]int, len(s.pending))
	for _, p := range s.pending {
		pendingBySession[p.SessionID]++
	}
	out := make([]*TrackedSession, 0, len(s.sessions))
	for _, ts := range s.sessions {
		cp := *ts
		// Streaming deltas rewrite lines in place, so readers get their own copy.
		cp.OutputLines = slices.Clone(ts.OutputLines)
		cp.PendingCount = pendingBySession[cp.ID]
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Busy != out[j].Busy {
			return out[i].Busy
		}
		return out[i].LastActiveAt.Before(out[j].LastActiveAt)
	})
	return out
}

func (s *Source) ActiveAgents() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	agents := make(map[string]bool)
	for sessionID := range s.busySessions {
		if a := s.sessionAgents[sessionID]; a != "" {
			agents[a] = true
		} else {
			agents["claude:"+sessionID] = true
		}
	}
	if len(agents) == 0 {
		return nil
	}
	return agents
}

func (s *Source) ActiveSessions() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.busySessions))
	for sessionID := range s.busySessions {
		out[sessionID] = s.sessionAgents[sessionID]
	}
	return out
}

func (s *Source) HasActiveAgents() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.busySessions) > 0
}

func (s *Source) OwnsSession(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[sessionID]
	return ok
}

// --- Output ---

// FetchSessionOutput is a no-op for Claude: the stream IS the authoritative
// transcript, so OutputLines are populated live by parse.go. The UI's fetch
// path uses FetchSessionOutputLines instead.
func (s *Source) FetchSessionOutput(sessionID string) {}

// FetchSessionOutputLines returns the in-memory OutputLines for a session. The
// `updated` flag is true only when new output has arrived since the last fetch
// (tracked via outputDirty), mirroring the opencode client's contract.
func (s *Source) FetchSessionOutputLines(sessionID string) ([]OutputLine, bool) {
	s.mu.Lock()
	ts, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, false
	}
	dirty := s.outputDirty[sessionID]
	cp := make([]OutputLine, len(ts.OutputLines))
	copy(cp, ts.OutputLines)
	s.outputDirty[sessionID] = false
	s.mu.Unlock()
	return cp, dirty
}

// SetSessionOutputLines is a no-op for Claude: parse.go owns OutputLines, so we
// never let the UI overwrite the live stream with a stale snapshot. We only
// clear the dirty flag (already cleared in FetchSessionOutputLines).
func (s *Source) SetSessionOutputLines(sessionID string, lines []OutputLine) {
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; ok {
		s.outputDirty[sessionID] = false
	}
	s.mu.Unlock()
}

// --- Messaging ---

// SendMessage spawns a follow-up `claude -p --resume` run for an existing
// session. The session flips back to Busy until the new run's result event.
// A claude subprocess can't service two concurrent turns on one session, so a
// message sent while Mirage's own turn is still running is queued and started
// when that turn ends. A session busy in a claude process Mirage doesn't own
// (an external terminal) can't be queued behind, so that is an error.
func (s *Source) SendMessage(sessionID, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("claude SendMessage: empty message")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("claude source is closed")
	}
	ts, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("claude SendMessage: unknown session %q", sessionID)
	}
	if s.busySessions[sessionID] {
		return s.queueLocked(ts, sessionID, text)
	}
	// If this is an externally discovered session (no Mirage subprocess),
	// stop tailing its transcript file: the resume subprocess we're about to
	// launch will own the stream, and we don't want the discoverer appending
	// duplicate lines from the same jsonl.
	delete(s.external, sessionID)
	s.mu.Unlock()
	go s.launchSession(text, sessionID, "")
	return nil
}

// queueLocked defers text until Mirage's running turn for the session ends. It
// releases s.mu. Callers must hold s.mu.
func (s *Source) queueLocked(ts *TrackedSession, sessionID, text string) error {
	if _, owned := s.procs[sessionID]; !owned {
		s.mu.Unlock()
		return fmt.Errorf("claude SendMessage: session %q is busy in another claude process", sessionID)
	}
	s.queued[sessionID] = append(s.queued[sessionID], text)
	ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "user", Text: "⏳ queued until the current turn finishes"})
	s.outputDirty[sessionID] = true
	s.mu.Unlock()
	s.fireEvent()
	return nil
}

// Launch starts a fresh Claude session with the given initial prompt. The
// working directory falls back to the source's configured WorkingDir when
// dir is empty. This is the entry point the UI's "New Claude session"
// launcher calls (a fresh session has no --resume target).
func (s *Source) Launch(prompt, dir string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("claude Launch: empty prompt")
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return fmt.Errorf("claude source is closed")
	}
	go s.launchSession(prompt, "", dir)
	return nil
}

// --- Lifecycle ---

func (s *Source) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.connected = false
	procs := s.procs
	s.procs = make(map[string]*sessionProc)
	s.pending = make(map[string]*Permission)
	s.pendingReqs = make(map[string]*pendingRequest)
	s.queued = make(map[string][]string)
	s.external = make(map[string]*externalTail)
	close(s.done)
	s.cancel()
	s.mu.Unlock()

	// Wait for the discoverer to stop touching s.sessions/s.external so the
	// map teardown below can't race with an in-flight poll. Only wait when the
	// discoverer actually started (Connect ran successfully).
	if s.discovererStarted {
		select {
		case <-s.discDone:
		case <-time.After(2 * time.Second):
		}
	}

	// Cancelling kills each subprocess; its scanStdout goroutine owns reaping
	// it (a second cmd.Wait here would race), so just wait for that to finish.
	deadline := time.After(2 * time.Second)
	for _, proc := range procs {
		proc.cancel()
	}
	for _, proc := range procs {
		select {
		case <-proc.done:
		case <-deadline:
			return
		}
	}
}
