package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// livenessTTL is how long after the last write a session file is still
// considered "running". Claude Code's on-disk transcript has no explicit
// terminal marker for interactive sessions (the TUI just stops appending on
// /exit), so mtime is the only pure-filesystem liveness signal. A session idle
// longer than this is dropped from the overview and re-surfaced if the user
// resumes it (mtime advances again). Long tool runs that write no records
// could exceed this; tunable if it becomes a problem.
const livenessTTL = 2 * time.Minute

// pollInterval is how often the discoverer rescans ~/.claude/projects.
const pollInterval = 2 * time.Second

// externalRetention is how long a tail's bookkeeping is kept after a session
// goes stale, so a brief flake (user pauses then resumes) doesn't force a full
// file replay. After this the tail is discarded and a reappearance replays.
const externalRetention = 10 * time.Minute

// externalTail tracks the read position and liveness of one externally
// discovered session file. It is owned by the discoverer goroutine; mutations
// to the session itself happen under s.mu, but the tail's own fields are only
// touched from that single goroutine.
type externalTail struct {
	path      string
	sessionID string
	offset    int64     // byte offset of the next unread byte (start of an unread line)
	residual  []byte    // trailing partial line (no newline yet)
	lastMod   time.Time // file mtime at last observation
	lastSeen  time.Time // last poll at which the file was fresh
	shown     bool      // currently registered in s.sessions
}

// transcriptRecord models the subset of Claude Code's on-disk transcript line
// that Mirage cares about. Each line is one JSON object; only the fields used
// for display are decoded, the rest is dropped. The on-disk format is NOT the
// stream-json format produced by `claude -p --output-format stream-json`
// (handled by parse.go) — it is a richer per-message transcript where every
// record carries sessionId/cwd/timestamp and a `message` object.
type transcriptRecord struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
}

// transcriptMessage is the `message` object inside assistant/user records.
// Content is a raw message because it may be a plain string (user prompt) or
// an array of typed blocks (text / tool_use / tool_result).
type transcriptMessage struct {
	Role       string           `json:"role"`
	Model      string           `json:"model,omitempty"`
	StopReason string           `json:"stop_reason,omitempty"`
	Usage      *transcriptUsage `json:"usage,omitempty"`
	Content    json.RawMessage  `json:"content,omitempty"`
}

type transcriptUsage struct {
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

// transcriptBlock is one element of an assistant/user content array.
type transcriptBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`     // tool_use
	IsError bool   `json:"is_error,omitempty"` // tool_result
}

// startDiscoverer launches the background poller that discovers Claude
// sessions started outside Mirage (e.g. an interactive `claude` TUI in another
// terminal). It scans ~/.claude/projects/*/*.jsonl for files still being
// appended to (mtime within livenessTTL), replays their transcript into
// TrackedSessions, and tails them live. Self-managed sessions (subprocesses
// Mirage launched, tracked in s.procs) are skipped to avoid double-tracking.
func (s *Source) startDiscoverer() {
	go func() {
		defer close(s.discDone)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-t.C:
				s.pollExternal()
			}
		}
	}()
}

// claudeProjectsDir returns ~/.claude/projects, where Claude Code persists one
// .jsonl transcript per session, grouped by encoded working directory.
func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// pollExternal performs one discovery + tail pass. It is the only writer to
// s.external (besides Close); per-session mutations it triggers happen under
// s.mu via applyTranscriptLines.
func (s *Source) pollExternal() {
	root := claudeProjectsDir()
	if root == "" {
		return
	}
	projects, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	seen := make(map[string]bool)

	for _, pe := range projects {
		if !pe.IsDir() {
			continue
		}
		subdir := filepath.Join(root, pe.Name())
		files, err := os.ReadDir(subdir)
		if err != nil {
			continue
		}
		for _, fe := range files {
			if fe.IsDir() || !strings.HasSuffix(fe.Name(), ".jsonl") {
				continue
			}
			sid := strings.TrimSuffix(fe.Name(), ".jsonl")
			// Skip anything Mirage is driving itself (running subprocess, or a
			// session we previously launched that has since completed). Those
			// are tracked via the subprocess stream, not the transcript file.
			if s.isSelfManaged(sid) {
				continue
			}
			fi, err := fe.Info()
			if err != nil {
				continue
			}
			mtime := fi.ModTime()
			if now.Sub(mtime) > livenessTTL {
				// Stale: if currently shown, drop it from the overview.
				s.dropExternal(sid)
				continue
			}
			seen[sid] = true
			s.tailExternal(sid, filepath.Join(subdir, fe.Name()), mtime, now)
		}
	}

	// Drop tails for files that disappeared (deleted or rotated) and any
	// stale tails that have outlived their retention.
	s.gcExternal(seen, now)
}

// isSelfManaged reports whether sid is a session Mirage itself owns (a running
// subprocess in s.procs, or a session it launched that's still in s.sessions
// but not managed as external). External sessions live in both s.sessions and
// s.external; the external set distinguishes them from self-completed ones.
func (s *Source) isSelfManaged(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.procs[sid]; ok {
		return true
	}
	_, inSess := s.sessions[sid]
	_, inExt := s.external[sid]
	if inSess && !inExt {
		return true
	}
	return false
}

// tailExternal ensures the session is registered and reads any new bytes from
// its transcript file since the last pass, applying each complete record line
// to the tracked session. On first observation the whole file is replayed.
func (s *Source) tailExternal(sid, path string, mtime, now time.Time) {
	s.mu.Lock()
	t, ok := s.external[sid]
	if !ok {
		t = &externalTail{path: path, sessionID: sid, lastMod: mtime, lastSeen: now}
		s.external[sid] = t
		if _, exists := s.sessions[sid]; !exists {
			s.sessions[sid] = &TrackedSession{
				ID:           sid,
				Title:        "Claude Session",
				Busy:         true,
				LastActiveAt: mtime,
			}
		}
		s.busySessions[sid] = true
		t.shown = true
		s.mu.Unlock()
		s.replayFile(t)
		s.fireEvent()
		return
	}
	s.mu.Unlock()

	t.lastMod = mtime
	t.lastSeen = now
	if !t.shown {
		// Re-surfacing after a stale drop: re-register and replay from the
		// start so the user sees the full transcript again.
		s.mu.Lock()
		if _, exists := s.sessions[sid]; !exists {
			s.sessions[sid] = &TrackedSession{ID: sid, Title: "Claude Session", Busy: true, LastActiveAt: mtime}
		}
		s.busySessions[sid] = true
		t.shown = true
		t.offset = 0
		t.residual = nil
		s.mu.Unlock()
		s.replayFile(t)
		s.fireEvent()
		return
	}
	s.replayFile(t)
}

// replayFile reads any new bytes from the tail's current offset (0 on first
// discovery) and applies every complete line, then fires one event. It serves
// both the initial full replay and incremental tailing: offset/residual
// track where the last pass left off.
func (s *Source) replayFile(t *externalTail) {
	lines := s.readNewLines(t)
	if len(lines) == 0 {
		return
	}
	changed := s.applyTranscriptLines(t.sessionID, lines)
	if changed {
		s.fireEvent()
	}
}

// readNewLines opens the transcript file, reads from the tail's offset to EOF,
// and returns all complete newline-terminated records. A trailing partial
// line (writer mid-flush) is kept in t.residual and re-read next pass; t.offset
// is advanced past the last fully-consumed byte.
func (s *Source) readNewLines(t *externalTail) [][]byte {
	f, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	st, err := f.Stat()
	if err != nil {
		return nil
	}
	// File truncated/replaced (e.g. cleared by the user): reset to start.
	if st.Size() < t.offset {
		t.offset = 0
		t.residual = nil
	}
	if _, err := f.Seek(t.offset, 0); err != nil {
		return nil
	}

	var chunk []byte
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk = append(chunk, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	all := append(t.residual, chunk...)
	idx := bytes.LastIndexByte(all, '\n')
	if idx < 0 {
		// No complete line yet; keep buffering.
		t.residual = all
		return nil
	}
	complete := all[:idx+1]
	t.residual = append([]byte(nil), all[idx+1:]...)
	t.offset += int64(len(complete))

	var lines [][]byte
	for len(complete) > 0 {
		nl := bytes.IndexByte(complete, '\n')
		lines = append(lines, append([]byte(nil), complete[:nl]...))
		complete = complete[nl+1:]
	}
	return lines
}

// applyTranscriptLines applies a batch of transcript records to one session
// under a single lock hold, inferring busy state from stop_reason as it goes.
// Returns true if any state changed worth redrawing.
func (s *Source) applyTranscriptLines(sid string, lines [][]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ts, ok := s.sessions[sid]
	if !ok {
		return false
	}
	changed := false
	for _, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Cwd != "" && ts.Directory == "" {
			ts.Directory = rec.Cwd
		}
		if rec.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
				ts.LastActiveAt = t
			}
		}
		switch rec.Type {
		case "user":
			c := s.applyUserRecord(ts, rec.Message)
			changed = changed || c
		case "assistant":
			c := s.applyAssistantRecord(ts, rec.Message)
			changed = changed || c
		case "system":
			// turn_duration / init / etc. — no transcript lines to surface.
			// init (if present) would carry cwd/version; we already took cwd.
		}
	}
	if changed {
		s.outputDirty[sid] = true
	}
	return changed
}

// applyUserRecord appends either the user's prompt (string content) or a tool
// completion marker (tool_result block). A user record always flips the
// session back to busy (the assistant is about to respond or continue).
func (s *Source) applyUserRecord(ts *TrackedSession, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var msg transcriptMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return false
	}
	content := msg.Content
	if len(content) == 0 {
		return false
	}
	// String content = a typed user prompt.
	if content[0] == '"' {
		var s2 string
		if err := json.Unmarshal(content, &s2); err != nil {
			return false
		}
		if strings.TrimSpace(s2) == "" {
			return false
		}
		if ts.Title == "" || ts.Title == "Claude Session" {
			ts.Title = truncateTitle(s2)
		}
		ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "user", Text: s2})
		ts.Busy = true
		ts.ToolState = ""
		return true
	}
	// Array content = one or more blocks (tool_result).
	var blocks []transcriptBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return false
	}
	any := false
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		mark := "  \u2713 done"
		if b.IsError {
			mark = "  \u2717 error"
		}
		ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "user", Text: mark})
		any = true
	}
	if any {
		ts.ToolState = "completed"
		ts.Busy = true
	}
	return any
}

// applyAssistantRecord appends text/tool_use blocks, records model + usage,
// and infers busy state from stop_reason: end_turn => idle, anything else
// (tool_use, max_tokens, ...) => still running.
func (s *Source) applyAssistantRecord(ts *TrackedSession, raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var msg transcriptMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return false
	}
	if msg.Model != "" {
		ts.ModelID = msg.Model
		ts.ContextWindow = contextWindowFor(msg.Model)
	}
	if msg.Usage != nil {
		ts.InputTokens = msg.Usage.InputTokens
		ts.OutputTokens = msg.Usage.OutputTokens
		ts.CacheRead = msg.Usage.CacheReadInputTokens
		ts.LastInputTokens = msg.Usage.InputTokens
		ts.LastCacheRead = msg.Usage.CacheReadInputTokens
	}
	if len(msg.Content) == 0 || msg.Content[0] != '[' {
		return false
	}
	var blocks []transcriptBlock
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return false
	}
	any := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			for _, line := range strings.Split(b.Text, "\n") {
				ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "assistant", Text: line})
			}
			any = true
		case "tool_use":
			if b.Name == "" {
				continue
			}
			ts.ToolName = b.Name
			ts.ToolState = "running"
			ts.OutputLines = append(ts.OutputLines, OutputLine{Role: "assistant", Text: "\u25b8 " + b.Name})
			any = true
		case "thinking":
			// Not surfaced.
		}
	}
	// stop_reason drives the busy flag. end_turn = idle awaiting user input;
	// tool_use = paused for a tool (still running); max_tokens/etc = running.
	ts.Busy = msg.StopReason != "" && msg.StopReason != "end_turn"
	if msg.StopReason == "end_turn" {
		ts.ToolState = ""
		ts.MessageCount++
	}
	return any
}

// dropExternal removes a session from the overview (it's no longer running)
// but keeps the tail's bookkeeping for externalRetention so a brief pause
// doesn't force a full replay on resume.
func (s *Source) dropExternal(sid string) {
	s.mu.Lock()
	t, ok := s.external[sid]
	if !ok {
		s.mu.Unlock()
		return
	}
	if t.shown {
		delete(s.sessions, sid)
		delete(s.busySessions, sid)
		delete(s.outputDirty, sid)
		delete(s.streamingIdx, sid)
		t.shown = false
	}
	s.mu.Unlock()
	s.fireEvent()
}

// gcExternal drops tails for files that vanished or went stale past retention.
func (s *Source) gcExternal(seen map[string]bool, now time.Time) {
	s.mu.Lock()
	var toDelete []string
	for sid, t := range s.external {
		if !seen[sid] {
			since := now.Sub(t.lastSeen)
			if since > externalRetention {
				toDelete = append(toDelete, sid)
			}
		}
	}
	for _, sid := range toDelete {
		delete(s.external, sid)
	}
	s.mu.Unlock()
}

// truncateTitle shortens a prompt to a display-friendly title length.
func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 60 {
		return s
	}
	return s[:57] + "..."
}
