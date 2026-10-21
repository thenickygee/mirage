package server

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// defaultSessionTitleRe matches opencode's auto-generated placeholder titles:
// "New session - 2026-05-01T12:34:56.789Z" and "Child session - ..."
var defaultSessionTitleRe = regexp.MustCompile(`^(?:New session|Child session) - \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// IsDefaultTitle reports whether s is an untitled opencode placeholder title.
func IsDefaultTitle(s string) bool {
	return s == "" || defaultSessionTitleRe.MatchString(s)
}

// DisplaySessionTitle returns a cleaned title for display. Default placeholder
// titles like "New session - 2026-..." are simplified to "New Session".
func DisplaySessionTitle(title string) string {
	if IsDefaultTitle(title) {
		return "New Session"
	}
	return title
}

// Source is the seam between the connection Pool and a concrete backend that
// provides sessions to monitor. The OpenCode HTTP client implements it
// (internal/server/opencode) and the Claude Code subprocess source will too
// (internal/server/claude). The Pool holds a map[string]Source and reaches
// into backends only through these methods, so direct struct-field access is
// replaced by interface calls.
type Source interface {
	// Identity
	ID() string   // baseURL for opencode, "claude:<pid>" for claude
	Kind() string // "opencode" | "claude"
	Connected() bool
	DisconnectedAt() *time.Time
	IsNewInstance() bool
	Connect() error
	Close()

	// Sessions
	TrackedSessions() []*TrackedSession
	ActiveAgents() map[string]bool
	ActiveSessions() map[string]string
	HasActiveAgents() bool
	OwnsSession(sessionID string) bool

	// Output
	FetchSessionOutput(sessionID string)
	FetchSessionOutputLines(sessionID string) ([]OutputLine, bool)
	SetSessionOutputLines(sessionID string, lines []OutputLine)

	// Permissions
	Pending() []*Permission
	PendingForSession(sessionID string) []*Permission
	PendingCount() int
	HasPermission(permID string) bool
	Respond(sessionID, permissionID, response string) error

	// Messaging
	SendMessage(sessionID, text string) error
}

type Permission struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Pattern   json.RawMessage        `json:"pattern,omitempty"`
	SessionID string                 `json:"sessionID"`
	MessageID string                 `json:"messageID"`
	CallID    string                 `json:"callID,omitempty"`
	Title     string                 `json:"title"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Time      struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

func (p *Permission) CreatedAt() time.Time {
	return time.UnixMilli(p.Time.Created)
}

func (p *Permission) PatternString() string {
	if len(p.Pattern) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(p.Pattern, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(p.Pattern, &arr) == nil {
		return strings.Join(arr, ", ")
	}
	return string(p.Pattern)
}

// OutputLine is a single rendered line tagged with the message role that
// produced it ("user" or "assistant").
type OutputLine struct {
	Role string
	Text string
}

// TrackedSession holds the live state of a session observed from a connected source.
type TrackedSession struct {
	ID              string
	Title           string
	Directory       string
	AgentName       string
	ModelID         string
	ToolName        string
	ToolState       string // state of the last tool (e.g. "running", "pending", "completed")
	Busy            bool
	WaitingForInput bool // true when idle but last tool is question/pending
	LastActiveAt    time.Time
	InputTokens     int64
	OutputTokens    int64
	CacheRead       int64
	Cost            float64
	MessageCount    int
	OutputLines     []OutputLine // live rendered output from message parts
	PendingCount    int          // number of pending permissions for this session
	ContextWindow   int64        // model context window size in tokens (0 = unknown)
	LastInputTokens int64        // input tokens from the most recent assistant message
	LastCacheRead   int64        // cache read tokens from the most recent assistant message
}
