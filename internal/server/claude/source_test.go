package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const fakePermissionScript = `#!/bin/sh
args="$*"
sid=""
resume=""
while [ $# -gt 0 ]; do
  case "$1" in
    --session-id) sid="$2" ;;
    --resume) resume="$2" ;;
  esac
  shift
done
echo "$args" >> "$FAKE_LOG"
read -r prompt
echo "$prompt" >> "$FAKE_LOG"
echo '{"type":"system","subtype":"init","session_id":"'"${sid:-$resume}"'","cwd":"/work","model":"claude-haiku-5-5"}'
if [ -z "$resume" ]; then
  echo '{"type":"control_request","request_id":"req-1","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"ls -la"},"description":"List files","permission_suggestions":[{"type":"setMode","mode":"acceptEdits","destination":"session"}],"tool_use_id":"toolu_1"}}'
  read -r resp
  printf '%s\n' "$resp" > "$FAKE_RESP"
fi
echo '{"type":"result","subtype":"success","is_error":false,"total_cost_usd":0.01}'
`

const fakeBigLineScript = `#!/bin/sh
read -r prompt
printf '{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"'
head -c 3000000 /dev/zero | tr '\0' 'a'
printf '"}]}}\n'
echo '{"type":"result","subtype":"success","is_error":false}'
`

// installFakeClaude puts a `claude` shell script first on $PATH and returns the
// scratch directory holding its log and captured permission response.
func installFakeClaude(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake claude is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOG", filepath.Join(dir, "log"))
	t.Setenv("FAKE_RESP", filepath.Join(dir, "resp"))
	return dir
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func onlySession(s *Source) *TrackedSession {
	sessions := s.TrackedSessions()
	if len(sessions) != 1 {
		return nil
	}
	return sessions[0]
}

func newTestSource(t *testing.T) *Source {
	t.Helper()
	s := NewSource(Config{AllowedTools: []string{"Read"}}, func() {})
	t.Cleanup(s.Close)
	return s
}

func TestBuildArgv(t *testing.T) {
	base := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages", "--permission-prompt-tool", "stdio",
	}
	t.Run("defaults omit bare and system prompt", func(t *testing.T) {
		got := buildArgv(launchConfig{Prompt: "hi"})
		if !slices.Equal(got, base) {
			t.Errorf("argv = %v, want %v", got, base)
		}
	})
	t.Run("bare is opt-in", func(t *testing.T) {
		if !slices.Contains(buildArgv(launchConfig{Prompt: "hi", Bare: true}), "--bare") {
			t.Error("--bare missing when Bare is set")
		}
	})
	t.Run("default permission mode defers to claude", func(t *testing.T) {
		if slices.Contains(buildArgv(launchConfig{Prompt: "hi", PermissionMode: "default"}), "--permission-mode") {
			t.Error("--permission-mode passed for \"default\"")
		}
	})
	t.Run("options are forwarded", func(t *testing.T) {
		got := strings.Join(buildArgv(launchConfig{
			Prompt: "hi", SessionID: "sid", ResumeID: "rid", Model: "haiku",
			PermissionMode: "acceptEdits", AllowedTools: []string{"Read", "Glob"},
			AppendSystemPrompt: "extra",
		}), " ")
		for _, want := range []string{
			"--session-id sid", "--resume rid", "--model haiku",
			"--permission-mode acceptEdits", "--allowedTools Read,Glob", "--append-system-prompt extra",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("argv %q missing %q", got, want)
			}
		}
	})
}

func TestLaunchAuthRequirement(t *testing.T) {
	installFakeClaude(t, fakePermissionScript)
	if _, err := launch(t.Context(), launchConfig{Prompt: "hi", Bare: true}); err == nil ||
		!strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("bare without key: err = %v, want ANTHROPIC_API_KEY error", err)
	}
	res, err := launch(t.Context(), launchConfig{Prompt: "hi"})
	if err != nil {
		t.Fatalf("non-bare launch without key failed: %v", err)
	}
	res.proc.cancel()
	_ = res.proc.cmd.Wait()
}

func TestLaunchMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := launch(t.Context(), launchConfig{Prompt: "hi"})
	if err == nil || !strings.Contains(err.Error(), "not found on $PATH") {
		t.Errorf("err = %v, want a clear missing-binary error", err)
	}
}

func TestPermissionRoundTrip(t *testing.T) {
	cases := []struct {
		response string
		check    func(t *testing.T, payload map[string]any)
	}{
		{"once", func(t *testing.T, p map[string]any) {
			if p["behavior"] != "allow" {
				t.Errorf("behavior = %v, want allow", p["behavior"])
			}
			if _, ok := p["updatedPermissions"]; ok {
				t.Error("once must not persist a permission rule")
			}
			if in, _ := p["updatedInput"].(map[string]any); in["command"] != "ls -la" {
				t.Errorf("updatedInput = %v, want original input echoed", p["updatedInput"])
			}
		}},
		{"always", func(t *testing.T, p map[string]any) {
			if p["behavior"] != "allow" {
				t.Errorf("behavior = %v, want allow", p["behavior"])
			}
			if sug, _ := p["updatedPermissions"].([]any); len(sug) != 1 {
				t.Errorf("updatedPermissions = %v, want claude's suggestion", p["updatedPermissions"])
			}
		}},
		{"reject", func(t *testing.T, p map[string]any) {
			if p["behavior"] != "deny" || p["message"] != rejectMessage {
				t.Errorf("payload = %v, want deny with message", p)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.response, func(t *testing.T) {
			dir := installFakeClaude(t, fakePermissionScript)
			s := newTestSource(t)
			if err := s.Launch("do it", dir); err != nil {
				t.Fatal(err)
			}

			eventually(t, "pending permission", func() bool { return s.PendingCount() == 1 })
			perm := s.Pending()[0]
			if perm.ID != "req-1" || perm.Type != "Bash" || perm.Title != "List files" || perm.PatternString() != "ls -la" {
				t.Errorf("permission = %+v (pattern %q)", perm, perm.PatternString())
			}
			if !s.HasPermission(perm.ID) {
				t.Error("HasPermission = false for a pending permission")
			}
			if ts := onlySession(s); ts == nil || ts.PendingCount != 1 || ts.ToolState != "pending" {
				t.Errorf("session = %+v, want PendingCount 1 and ToolState pending", ts)
			}

			if err := s.Respond(perm.SessionID, perm.ID, tc.response); err != nil {
				t.Fatal(err)
			}
			eventually(t, "turn to finish", func() bool { ts := onlySession(s); return ts != nil && !ts.Busy })
			if s.PendingCount() != 0 {
				t.Error("permission still pending after Respond")
			}

			raw, err := os.ReadFile(filepath.Join(dir, "resp"))
			if err != nil {
				t.Fatal(err)
			}
			var msg struct {
				Type     string `json:"type"`
				Response struct {
					Subtype   string         `json:"subtype"`
					RequestID string         `json:"request_id"`
					Response  map[string]any `json:"response"`
				} `json:"response"`
			}
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("response %q is not JSON: %v", raw, err)
			}
			if msg.Type != "control_response" || msg.Response.Subtype != "success" || msg.Response.RequestID != "req-1" {
				t.Errorf("envelope = %+v", msg)
			}
			tc.check(t, msg.Response.Response)
		})
	}
}

func TestFollowUpQueuedWhileTurnRuns(t *testing.T) {
	dir := installFakeClaude(t, fakePermissionScript)
	s := newTestSource(t)
	if err := s.Launch("first", dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pending permission", func() bool { return s.PendingCount() == 1 })
	sid := s.Pending()[0].SessionID

	if err := s.SendMessage(sid, "second"); err != nil {
		t.Fatalf("SendMessage while busy = %v, want it queued", err)
	}
	if err := s.Respond(sid, "req-1", "once"); err != nil {
		t.Fatal(err)
	}

	eventually(t, "queued turn to run", func() bool {
		log, _ := os.ReadFile(filepath.Join(dir, "log"))
		return strings.Contains(string(log), "--resume "+sid) && strings.Contains(string(log), `"content":"second"`)
	})
	eventually(t, "queued turn to finish", func() bool { ts := onlySession(s); return ts != nil && !ts.Busy })
}

func TestSendMessageToExternalBusySessionErrors(t *testing.T) {
	s := newTestSource(t)
	s.sessions["ext"] = &TrackedSession{ID: "ext", Busy: true}
	s.busySessions["ext"] = true
	if err := s.SendMessage("ext", "hi"); err == nil {
		t.Error("expected an error for a session busy in another process")
	}
}

func TestLargeStreamLineDoesNotStallSession(t *testing.T) {
	dir := installFakeClaude(t, fakeBigLineScript)
	s := newTestSource(t)
	if err := s.Launch("read it", dir); err != nil {
		t.Fatal(err)
	}
	eventually(t, "turn to finish", func() bool { ts := onlySession(s); return ts != nil && !ts.Busy && len(ts.OutputLines) > 0 })

	lines := onlySession(s).OutputLines
	if got := len(lines[0].Text); got != 3_000_000 {
		t.Errorf("first line length = %d, want 3000000", got)
	}
}

func TestResultErrorSurfaced(t *testing.T) {
	line := func(msg string) []byte {
		b, _ := json.Marshal(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": msg})
		return b
	}
	t.Run("is_error under success subtype", func(t *testing.T) {
		s := newTestSource(t)
		s.sessions["a"] = &TrackedSession{ID: "a", Busy: true}
		s.handleLine(line("Invalid API key"), "a", nil)
		out := s.sessions["a"].OutputLines
		if len(out) != 1 || out[0].Text != "✗ Invalid API key" {
			t.Errorf("lines = %+v, want one ✗ line", out)
		}
	})
	t.Run("not repeated when already streamed as assistant text", func(t *testing.T) {
		s := newTestSource(t)
		s.sessions["a"] = &TrackedSession{ID: "a", Busy: true, OutputLines: []OutputLine{{Role: "assistant", Text: "Invalid API key"}}}
		s.handleLine(line("Invalid API key"), "a", nil)
		if n := len(s.sessions["a"].OutputLines); n != 1 {
			t.Errorf("got %d lines, want the failure shown once", n)
		}
	})
}

func TestUnsupportedControlRequestIsAnswered(t *testing.T) {
	var buf bytes.Buffer
	proc := &sessionProc{stdin: nopWriteCloser{&buf}}
	s := newTestSource(t)
	s.handleLine([]byte(`{"type":"control_request","request_id":"r9","request":{"subtype":"mcp_message"}}`), "a", proc)

	var msg map[string]any
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("no JSON reply written: %q (%v)", buf.String(), err)
	}
	if r, _ := msg["response"].(map[string]any); r["subtype"] != "error" || r["request_id"] != "r9" {
		t.Errorf("reply = %v, want an error response for r9", msg)
	}
	if s.PendingCount() != 0 {
		t.Error("unsupported request must not appear as a pending permission")
	}
}

func TestControlCancelDropsPermission(t *testing.T) {
	s := newTestSource(t)
	proc := &sessionProc{stdin: nopWriteCloser{io.Discard}}
	s.handleLine([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"ls"}}}`), "a", proc)
	if s.PendingCount() != 1 {
		t.Fatal("permission not recorded")
	}
	s.handleLine([]byte(`{"type":"control_cancel_request","request_id":"r1"}`), "a", proc)
	if s.PendingCount() != 0 {
		t.Error("permission still pending after cancel")
	}
}

func TestUnknownEventsAreIgnored(t *testing.T) {
	s := newTestSource(t)
	for _, l := range []string{
		`{"type":"rate_limit_event","rate_limit_info":{}}`,
		`{"type":"system","subtype":"hook_started","hook_id":"x"}`,
		`{"type":"brand_new_type","payload":1}`,
		`not json at all`,
	} {
		if got := s.handleLine([]byte(l), "a", nil); got != "a" {
			t.Errorf("handleLine(%q) re-keyed session to %q", l, got)
		}
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// TestLiveClaudeTurn drives one real claude turn using claude's own login (no
// --bare, no key). It is skipped unless MIRAGE_LIVE_CLAUDE=1 because it spends
// a few tokens; it guards against stream-json / control protocol drift.
func TestLiveClaudeTurn(t *testing.T) {
	if os.Getenv("MIRAGE_LIVE_CLAUDE") != "1" {
		t.Skip("set MIRAGE_LIVE_CLAUDE=1 to run against the real claude CLI")
	}
	dir := t.TempDir()
	s := NewSource(Config{Model: "haiku"}, func() {})
	t.Cleanup(s.Close)
	if err := s.Launch("Use the Write tool to create live_probe.txt containing hi. Nothing else.", dir); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(90 * time.Second)
	answered := false
	for time.Now().Before(deadline) {
		for _, p := range s.Pending() {
			t.Logf("permission: %s %q %q", p.Type, p.Title, p.PatternString())
			if err := s.Respond(p.SessionID, p.ID, "once"); err != nil {
				t.Fatal(err)
			}
			answered = true
		}
		if ts := onlySession(s); ts != nil && !ts.Busy && ts.MessageCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	ts := onlySession(s)
	if ts == nil || ts.Busy {
		t.Fatalf("turn did not finish: %+v", ts)
	}
	for _, l := range ts.OutputLines {
		if strings.HasPrefix(l.Text, "✗") {
			t.Errorf("error line in transcript: %q", l.Text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "live_probe.txt")); err != nil {
		t.Errorf("file not written (permission answered=%v): %v", answered, err)
	}
	t.Logf("permission prompted=%v lines=%d cost=$%.4f", answered, len(ts.OutputLines), ts.Cost)
}
