package claude

import (
	"testing"
)

// TestDiscoverPollSmoke runs one discovery pass against the user's real
// ~/.claude/projects dir to confirm the parser is robust against live data.
// It does not assert specifics (the dir is environment-dependent); it just
// must not panic and should leave a consistent in-memory state.
func TestDiscoverPollSmoke(t *testing.T) {
	s := NewSource(Config{}, func() {})
	// Skip the subprocess/InitialPrompt path; just exercise the discoverer
	// logic synchronously.
	s.pollExternal()
	out := s.TrackedSessions()
	t.Logf("tracked %d external sessions", len(out))
	for _, ts := range out {
		if ts.ID == "" {
			t.Errorf("session with empty ID")
		}
		if len(ts.OutputLines) == 0 && ts.Directory == "" {
			t.Logf("session %s: empty (likely no fresh activity)", ts.ID)
		}
	}
	_ = out
}
