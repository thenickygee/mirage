package claude

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// launchConfig describes a single `claude -p` invocation. One launch = one
// streaming subprocess whose NDJSON stdout is parsed by parse.go.
type launchConfig struct {
	Prompt             string   // initial user message (required for fresh sessions)
	ResumeID           string   // session ID to resume; empty for a fresh session
	SessionID          string   // pre-generated UUID passed via --session-id
	Model              string   // optional model override
	PermissionMode     string   // acceptEdits | dontAsk | default | "" (claude default)
	AllowedTools       []string // optional --allowedTools list
	APIKey             string   // ANTHROPIC_API_KEY (required under --bare)
	WorkingDir         string   // cwd for the subprocess
	AppendSystemPrompt string   // appended to the default system prompt
}

// buildArgv constructs the `claude` argv from a launchConfig.
//
//	claude --bare -p <prompt>
//	  --output-format stream-json
//	  --verbose
//	  --include-partial-messages
//	  --session-id <uuid>
//	  [--resume <sessionID>]
//	  [--allowedTools "Read,Edit,Bash"]
//	  [--permission-mode acceptEdits]
//	  [--model <id>]
//	  --append-system-prompt "<text>"
func buildArgv(cfg launchConfig) []string {
	args := []string{
		"--bare",
		"-p", cfg.Prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
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
	if cfg.PermissionMode != "" {
		args = append(args, "--permission-mode", cfg.PermissionMode)
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	// --append-system-prompt is always required under --bare (the plan passes
	// an empty string). Keep it last for readability.
	args = append(args, "--append-system-prompt", cfg.AppendSystemPrompt)
	return args
}

// launchResult holds the running process and its pipes.
type launchResult struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// launch spawns a `claude -p` subprocess for the given config and returns its
// stdout/stderr pipes for streaming. The subprocess inherits the parent's env
// plus ANTHROPIC_API_KEY when provided. stdin is wired to a reader that
// immediately sends EOF so the subprocess can exit cleanly.
func launch(ctx context.Context, cfg launchConfig) (*launchResult, error) {
	if cfg.APIKey == "" {
		// --bare never reads OAuth/keychain; the key is mandatory.
		return nil, fmt.Errorf("claude --bare requires ANTHROPIC_API_KEY (set -claude-key or $ANTHROPIC_API_KEY)")
	}
	if cfg.Prompt == "" && cfg.ResumeID == "" {
		return nil, fmt.Errorf("claude launch requires either a prompt or a resume ID")
	}

	cmd := exec.CommandContext(ctx, "claude", buildArgv(cfg)...)
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	// Pass through the parent environment plus the API key. Also forward any
	// CLAUDE_CODE_* overrides the user may have set.
	cmd.Env = append(os.Environ(), "ANTHROPIC_API_KEY="+cfg.APIKey)

	// stdin: an empty reader that closes immediately so the subprocess sees EOF.
	cmd.Stdin = strings.NewReader("")

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, fmt.Errorf("claude stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	return &launchResult{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

// newUUID generates a version-4 UUID string suitable for --session-id.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
