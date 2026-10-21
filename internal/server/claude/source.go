package claude

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	APIKey         string   // ANTHROPIC_API_KEY; falls back to $ANTHROPIC_API_KEY
	Model          string   // optional --model override
	PermissionMode string   // acceptEdits | dontAsk | default | ""
	AllowedTools   []string // optional --allowedTools list
	WorkingDir     string   // cwd for launched subprocesses
	InitialPrompt  string   // optional: launch a session at startup (Phase 2 smoke test)
}

// Source implements server.Source for Claude Code sessions. Each session is a
// separate `claude --bare -p … --output-format stream-json` subprocess whose
// NDJSON stdout is parsed by parse.go into TrackedSession state. The source
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
	pending       map[string]*Permission     // always empty until Phase 4
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

// sessionProc tracks one running `claude -p` subprocess.
type sessionProc struct {
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stderr     io.ReadCloser
	errBuf     *strings.Builder // captured stderr for diagnostics
	stderrDone chan struct{}    // closed when drainStderr finishes
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
	if key == "" {
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
		Prompt:             prompt,
		ResumeID:           resumeID,
		SessionID:          sidFlag,
		Model:              s.cfg.Model,
		PermissionMode:     s.cfg.PermissionMode,
		AllowedTools:       s.cfg.AllowedTools,
		APIKey:             key,
		WorkingDir:         workingDir,
		AppendSystemPrompt: "",
	})
	if err != nil {
		s.mu.Lock()
		if resumeID != "" {
			// Follow-up launch failed: surface the error on the existing
			// session rather than creating a phantom new one.
			if ts, ok := s.sessions[resumeID]; ok {
				ts.OutputLines = append(ts.OutputLines, OutputLine{
					Role: "assistant",
					Text: "✗ " + err.Error(),
				})
				s.outputDirty[resumeID] = true
			}
		} else {
			s.sessions[sessionID] = &TrackedSession{
				ID:        sessionID,
				Title:     "Claude Session",
				Directory: workingDir,
				Busy:      false,
				OutputLines: []OutputLine{{
					Role: "assistant",
					Text: "✗ " + err.Error(),
				}},
			}
		}
		s.mu.Unlock()
		s.fireEvent()
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = res.cmd.Process.Kill()
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
	procCtx, procCancel := context.WithCancel(s.ctx)
	proc := &sessionProc{
		cmd:        res.cmd,
		cancel:     procCancel,
		stderr:     res.stderr,
		errBuf:     &strings.Builder{},
		stderrDone: make(chan struct{}),
	}
	s.procs[sessionID] = proc
	s.mu.Unlock()
	s.fireEvent()

	// Drain stderr (diagnostics) in the background.
	go s.drainStderr(sessionID, proc)
	// Scan stdout NDJSON and dispatch events.
	go s.scanStdout(sessionID, procCtx, procCancel, res)
}

// scanStdout reads the claude subprocess's NDJSON stdout line-by-line and
// dispatches each line to handleLine. On EOF or scanner error, it finalizes
// the session if no result event was received (unexpected exit).
func (s *Source) scanStdout(sessionID string, procCtx context.Context, procCancel context.CancelFunc, res *launchResult) {
	defer procCancel()
	scanner := bufio.NewScanner(res.stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	sid := sessionID
	for scanner.Scan() {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		sid = s.handleLine(line, sid)
	}
	// stdout closed: the process has exited. Wait for it and, if the session is
	// still busy (no result event), mark it idle with an error.
	exitErr := res.cmd.Wait()
	s.mu.Lock()
	ts, ok := s.sessions[sid]
	stillBusy := ok && s.busySessions[sid]
	if stillBusy {
		ts.Busy = false
		ts.WaitingForInput = false
		ts.ToolState = ""
		ts.LastActiveAt = time.Now()
	}
	s.mu.Unlock()
	// Wait for drainStderr to finish so errBuf has the complete stderr output.
	s.mu.Lock()
	proc := s.procs[sid]
	s.mu.Unlock()
	if proc != nil && proc.stderrDone != nil {
		<-proc.stderrDone
	}
	s.mu.Lock()
	var stderrText string
	if proc != nil {
		stderrText = strings.TrimSpace(proc.errBuf.String())
	}
	delete(s.procs, sid)
	s.mu.Unlock()
	if stillBusy {
		// Prefer stderr text (contains the real error message from claude)
		// over exitErr.Error() which is just "exit status 1".
		msg := stderrText
		if msg == "" {
			if exitErr != nil {
				msg = exitErr.Error()
			} else {
				msg = "claude process exited unexpectedly"
			}
		}
		s.mu.Lock()
		if ts != nil {
			ts.OutputLines = append(ts.OutputLines, OutputLine{
				Role: "assistant",
				Text: "✗ " + msg,
			})
			s.outputDirty[sid] = true
		}
		s.mu.Unlock()
		s.fireEvent()
	}
}

// drainStderr copies the claude subprocess's stderr into the proc's errBuf for
// later surfacing on unexpected exit.
func (s *Source) drainStderr(sessionID string, proc *sessionProc) {
	defer close(proc.stderrDone)
	buf := make([]byte, 4096)
	for {
		n, err := proc.stderr.Read(buf)
		if n > 0 {
			proc.errBuf.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// --- Sessions ---

func (s *Source) TrackedSessions() []*TrackedSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*TrackedSession, 0, len(s.sessions))
	for _, ts := range s.sessions {
		cp := *ts
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

// --- Permissions (no-op until Phase 4) ---

func (s *Source) Pending() []*Permission                                 { return nil }
func (s *Source) PendingForSession(string) []*Permission                 { return nil }
func (s *Source) PendingCount() int                                      { return 0 }
func (s *Source) HasPermission(string) bool                              { return false }
func (s *Source) Respond(sessionID, permissionID, response string) error { return nil }

// --- Messaging ---

// SendMessage spawns a follow-up `claude -p --resume` run for an existing
// session. The session flips back to Busy until the new run's result event.
// Returns an error if the session is unknown or already busy (a claude
// subprocess can't service two concurrent turns on one session).
func (s *Source) SendMessage(sessionID, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("claude SendMessage: empty message")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("claude source is closed")
	}
	_, ok := s.sessions[sessionID]
	busy := s.busySessions[sessionID]
	// If this is an externally discovered session (no Mirage subprocess),
	// stop tailing its transcript file: the resume subprocess we're about to
	// launch will own the stream, and we don't want the discoverer appending
	// duplicate lines from the same jsonl.
	delete(s.external, sessionID)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("claude SendMessage: unknown session %q", sessionID)
	}
	if busy {
		return fmt.Errorf("claude SendMessage: session %q is already running", sessionID)
	}
	go s.launchSession(text, sessionID, "")
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

	for _, proc := range procs {
		proc.cancel()
		if proc.cmd != nil && proc.cmd.Process != nil {
			_ = proc.cmd.Process.Kill()
		}
		_ = proc.cmd.Wait()
	}
}
