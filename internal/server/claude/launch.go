package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// launchConfig describes a single `claude -p` invocation. One launch = one
// streaming subprocess whose NDJSON stdout is parsed by parse.go and whose
// stdin carries the user prompt plus permission answers.
type launchConfig struct {
	Prompt             string   // user message sent on stdin (required)
	ResumeID           string   // session ID to resume; empty for a fresh session
	SessionID          string   // pre-generated UUID passed via --session-id
	Model              string   // optional model override
	PermissionMode     string   // acceptEdits | auto | dontAsk | plan | default | "" (claude default)
	AllowedTools       []string // optional --allowedTools list
	Bare               bool     // run with --bare: API-key auth only, no hooks/CLAUDE.md/plugins
	APIKey             string   // ANTHROPIC_API_KEY (required when Bare, optional override otherwise)
	WorkingDir         string   // cwd for the subprocess
	AppendSystemPrompt string   // appended to the default system prompt
}

// buildArgv constructs the `claude` argv from a launchConfig.
//
//	claude -p
//	  --input-format stream-json
//	  --output-format stream-json
//	  --verbose
//	  --include-partial-messages
//	  --permission-prompt-tool stdio
//	  [--bare]
//	  [--session-id <uuid>]
//	  [--resume <sessionID>]
//	  [--allowedTools "Read,Glob,Grep"]
//	  [--permission-mode acceptEdits]
//	  [--model <id>]
//	  [--append-system-prompt "<text>"]
//
// The prompt travels on stdin (see userMessage) so the same pipe can answer
// can_use_tool control requests while the turn is running.
func buildArgv(cfg launchConfig) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
	}
	if cfg.Bare {
		args = append(args, "--bare")
	}
	if cfg.SessionID != "" {
		args = append(args, "--session-id", cfg.SessionID)
	}
	if cfg.ResumeID != "" {
		args = append(args, "--resume", cfg.ResumeID)
	}
	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(cfg.AllowedTools, ","))
	}
	if mode := normalizePermissionMode(cfg.PermissionMode); mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", cfg.AppendSystemPrompt)
	}
	return args
}

// normalizePermissionMode maps "default" to claude's own default by omitting
// the flag: newer CLIs renamed that mode and reject the old spelling.
func normalizePermissionMode(mode string) string {
	if mode == "default" {
		return ""
	}
	return mode
}

// launchEnv returns the subprocess environment. Outside --bare, claude uses its
// own login (OAuth/keychain or the inherited env), so a key is only injected
// when explicitly configured.
func launchEnv(cfg launchConfig) []string {
	env := os.Environ()
	if cfg.APIKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+cfg.APIKey)
	}
	return env
}

// sessionProc is one running `claude -p` subprocess. Writes to stdin are
// serialized because permission answers come from the UI goroutine while the
// initial prompt is written by the launcher.
type sessionProc struct {
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	stderr     io.ReadCloser
	errBuf     *lockedBuffer // captured stderr for diagnostics
	stderrDone chan struct{} // closed when drainStderr finishes
	done       chan struct{} // closed when scanStdout has reaped the process

	stdinMu sync.Mutex
	stdin   io.WriteCloser
}

// lockedBuffer is a strings.Builder safe for the stderr drainer to write while
// the scanner reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var errStdinClosed = errors.New("claude process is no longer accepting input")

// send writes one NDJSON message to the subprocess's stdin.
func (p *sessionProc) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encoding claude message: %w", err)
	}
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdin == nil {
		return errStdinClosed
	}
	if _, err := p.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("writing to claude stdin: %w", err)
	}
	return nil
}

// closeStdin sends EOF so claude exits once the current turn is finished.
func (p *sessionProc) closeStdin() {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
}

// userMessage is the stream-json envelope for a user turn.
func userMessage(prompt string) map[string]any {
	return map[string]any{
		"type":               "user",
		"message":            map[string]any{"role": "user", "content": prompt},
		"parent_tool_use_id": nil,
		"session_id":         "",
	}
}

// controlResponse is the stream-json envelope answering a control_request.
func controlResponse(requestID, errMsg string, payload map[string]any) map[string]any {
	resp := map[string]any{"request_id": requestID}
	if errMsg != "" {
		resp["subtype"] = "error"
		resp["error"] = errMsg
	} else {
		resp["subtype"] = "success"
		resp["response"] = payload
	}
	return map[string]any{"type": "control_response", "response": resp}
}

// launchResult holds the running process and its pipes.
type launchResult struct {
	proc   *sessionProc
	stdout io.ReadCloser
}

// launch spawns a `claude -p` subprocess for the given config, writes the
// initial prompt to its stdin, and returns the process and stdout pipe for
// streaming. stdin stays open so permission answers can be sent mid-turn; the
// caller closes it once the turn's result arrives.
func launch(ctx context.Context, cfg launchConfig) (*launchResult, error) {
	if cfg.Bare && cfg.APIKey == "" {
		return nil, errors.New("claude --bare requires ANTHROPIC_API_KEY (set -claude-key or $ANTHROPIC_API_KEY)")
	}
	if cfg.Prompt == "" {
		return nil, errors.New("claude launch requires a prompt")
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("claude CLI not found on $PATH: install Claude Code and make sure `claude` is runnable")
	}

	procCtx, procCancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, path, buildArgv(cfg)...)
	cmd.Dir = cfg.WorkingDir
	cmd.Env = launchEnv(cfg)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return nil, fmt.Errorf("claude stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		procCancel()
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		procCancel()
		return nil, fmt.Errorf("claude stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		procCancel()
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	proc := &sessionProc{
		cmd:        cmd,
		cancel:     procCancel,
		stderr:     stderr,
		errBuf:     &lockedBuffer{},
		stderrDone: make(chan struct{}),
		done:       make(chan struct{}),
		stdin:      stdin,
	}
	if err := proc.send(userMessage(cfg.Prompt)); err != nil {
		procCancel()
		_ = cmd.Wait()
		return nil, err
	}
	return &launchResult{proc: proc, stdout: stdout}, nil
}

// newUUID generates a version-4 UUID string suitable for --session-id.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
