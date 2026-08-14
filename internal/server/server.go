
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// subagentTitleRe matches titles like "Explore project structure (@explore subagent)"
var subagentTitleRe = regexp.MustCompile(`\(@([\w-]+)(?:\s+subagent)?\)`)

// defaultSessionTitleRe matches opencode's auto-generated placeholder titles:
// "New session - 2026-05-01T12:34:56.789Z" and "Child session - ..."
var defaultSessionTitleRe = regexp.MustCompile(`^(?:New session|Child session) - \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// isDefaultTitle reports whether s is an untitled opencode placeholder title.
func isDefaultTitle(s string) bool {
	return s == "" || defaultSessionTitleRe.MatchString(s)
}

// DisplaySessionTitle returns a cleaned title for display. Default placeholder
// titles like "New session - 2026-..." are simplified to "New Session".
func DisplaySessionTitle(title string) string {
	if isDefaultTitle(title) {
		return "New Session"
	}
	return title
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

type sseEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

// OutputLine is a single rendered line tagged with the message role that
// produced it ("user" or "assistant").
type OutputLine struct {
	Role string
	Text string
}

// TrackedSession holds the live state of a session observed from a connected server.
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

type Client struct {
	baseURL        string
	mu             sync.Mutex
	pending        map[string]*Permission     // permissionID -> Permission
	busySessions   map[string]bool            // sessionID -> busy
	sessionAgents  map[string]string          // sessionID -> agent name
	sessions       map[string]*TrackedSession // sessionID -> session state
	msgTokens      map[string]msgTokenSnap    // messageID -> last seen tokens (for delta)
	lastOutputCnt  map[string]int             // sessionID -> last fetched message count (skip redundant parses)
	outputDirty    map[string]bool            // sessionID -> true when SSE saw new messages since last fetch
	modelLimits    map[string]int64           // modelID -> context window size in tokens
	onEvent        func()                     // callback when state changes
	done           chan struct{}
	cancel         context.CancelFunc
	ctx            context.Context
	closed         bool
	connected      bool
	disconnectedAt *time.Time // set when connection drops, cleared on reconnect
	isNewInstance  bool       // true until first session.created event is received
}

// msgTokenSnap stores the last seen token/cost values for a message so we can
// compute deltas on message.updated events (which fire multiple times).
type msgTokenSnap struct {
	sessionID    string
	inputTokens  int64
	outputTokens int64
	cacheRead    int64
	cost         float64
}

func New(baseURL string, onEvent func()) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		baseURL:       baseURL,
		pending:       make(map[string]*Permission),
		busySessions:  make(map[string]bool),
		sessionAgents: make(map[string]string),
		sessions:      make(map[string]*TrackedSession),
		msgTokens:     make(map[string]msgTokenSnap),
		lastOutputCnt: make(map[string]int),
		outputDirty:   make(map[string]bool),
		modelLimits:   make(map[string]int64),
		onEvent:       onEvent,
		done:          make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
		isNewInstance: true,
	}
}

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// DisconnectedAt returns the time the client last lost its connection, or nil
// if it is currently connected (or has never connected).
func (c *Client) DisconnectedAt() *time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnectedAt
}

func (c *Client) IsNewInstance() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.isNewInstance
}

// ActiveAgents returns the set of agent names that currently have a busy session.
// This is derived from SSE events — no HTTP polling is performed.
// For busy sessions where the agent name is not yet known, the session ID is
// returned prefixed with "ses:" so callers can still show activity.
func (c *Client) ActiveAgents() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	agents := make(map[string]bool)
	for sessionID := range c.busySessions {
		if agent, ok := c.sessionAgents[sessionID]; ok && agent != "" {
			agents[agent] = true
		} else {
			agents["ses:"+sessionID] = true
		}
	}
	if len(agents) == 0 {
		return nil
	}
	return agents
}

// ActiveSessions returns a map of sessionID -> agentName for all currently busy sessions.
// If the agent name is not yet known for a session, the value will be an empty string.
func (c *Client) ActiveSessions() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.busySessions))
	for sessionID := range c.busySessions {
		out[sessionID] = c.sessionAgents[sessionID]
	}
	return out
}

// TrackedSessions returns a snapshot of all sessions known to this client,
// sorted by most recently updated (busy sessions first, then by title).
func (c *Client) TrackedSessions() []*TrackedSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Count pending permissions per session.
	pendingBySession := make(map[string]int, len(c.pending))
	for _, p := range c.pending {
		pendingBySession[p.SessionID]++
	}
	out := make([]*TrackedSession, 0, len(c.sessions))
	for _, s := range c.sessions {
		cp := *s
		cp.PendingCount = pendingBySession[cp.ID]
		out = append(out, &cp)
	}
	// Sort: busy first, then by most recently active
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			swap := false
			if a.Busy != b.Busy {
				swap = !a.Busy && b.Busy
			} else {
				swap = a.LastActiveAt.Before(b.LastActiveAt)
			}
			if swap {
				out[j-1], out[j] = out[j], out[j-1]
			} else {
				break
			}
		}
	}
	return out
}

// HasActiveAgents reports whether any sessions are currently busy.
func (c *Client) HasActiveAgents() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.busySessions) > 0
}

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) Pending() []*Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Permission, 0, len(c.pending))
	for _, p := range c.pending {
		out = append(out, p)
	}
	return out
}

func (c *Client) PendingForSession(sessionID string) []*Permission {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*Permission
	for _, p := range c.pending {
		if p.SessionID == sessionID {
			out = append(out, p)
		}
	}
	return out
}

func (c *Client) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *Client) Respond(sessionID, permissionID, response string) error {
	body := fmt.Sprintf(`{"response":"%s"}`, response)
	url := fmt.Sprintf("%s/session/%s/permissions/%s", c.baseURL, sessionID, permissionID)
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	c.mu.Lock()
	delete(c.pending, permissionID)
	c.mu.Unlock()
	return nil
}

// SendMessage sends a text message to an active session via the opencode API.
func (c *Client) SendMessage(sessionID, text string) error {
	body := fmt.Sprintf(`{"parts":[{"type":"text","text":%s}]}`, jsonEscapeString(text))
	url := fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

// jsonEscapeString returns a JSON-encoded string value (with quotes).
func jsonEscapeString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// fetchModelLimits calls /provider and builds a modelID -> context-window map.
func (c *Client) fetchModelLimits() {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.baseURL+"/provider", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var payload struct {
		All []struct {
			Models map[string]struct {
				Limit struct {
					Context int64 `json:"context"`
				} `json:"limit"`
			} `json:"models"`
		} `json:"all"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return
	}
	limits := make(map[string]int64)
	for _, provider := range payload.All {
		for modelID, model := range provider.Models {
			if model.Limit.Context > 0 {
				limits[modelID] = model.Limit.Context
			}
		}
	}
	c.mu.Lock()
	c.modelLimits = limits
	c.mu.Unlock()
}

func (c *Client) Connect() error {
	resp, err := http.Get(c.baseURL + "/global/health")
	if err != nil {
		return fmt.Errorf("cannot reach server: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server health check returned %d", resp.StatusCode)
	}
	c.fetchModelLimits()
	c.seedLatestSession()
	c.seedSessionStatus()
	c.seedPendingQuestions()
	go c.streamEvents()
	return nil
}

// seedSessionStatus polls /session/status once at connect time to capture any
// sessions that are already busy before we started listening to SSE events.
func (c *Client) seedSessionStatus() {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.baseURL+"/session/status", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return
	}
	c.mu.Lock()
	if len(statuses) > 0 {
		c.isNewInstance = false
	}
	for sessionID, s := range statuses {
		busy := s.Type == "busy"
		if busy {
			c.busySessions[sessionID] = true
		}
		if ts, ok := c.sessions[sessionID]; ok {
			ts.Busy = busy
		} else {
			c.sessions[sessionID] = &TrackedSession{ID: sessionID, Busy: busy}
		}
	}
	c.mu.Unlock()

	// Fetch title/directory, token totals, and agent info for each session.
	for sessionID := range statuses {
		c.seedSessionInfo(sessionID)
	}
}

// seedPendingQuestions polls GET /question at connect time to detect any
// sessions that are already waiting for user input.
func (c *Client) seedPendingQuestions() {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.baseURL+"/question", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var questions []struct {
		SessionID string `json:"sessionID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&questions); err != nil {
		return
	}
	c.mu.Lock()
	for _, q := range questions {
		if q.SessionID == "" {
			continue
		}
		if ts, ok := c.sessions[q.SessionID]; ok {
			ts.WaitingForInput = true
			ts.ToolName = "question"
			ts.ToolState = "running"
		}
	}
	c.mu.Unlock()
}

// seedLatestSession fetches the most recently updated root session from the
// server and seeds it with full info and token data. This ensures idle sessions
// are discovered on startup even though /session/status omits them.
func (c *Client) seedLatestSession() {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.baseURL+"/session?roots=true&limit=1", nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var sessions []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Path      string `json:"path"`
		Info      struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Directory string `json:"directory"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return
	}
	if len(sessions) == 0 {
		return
	}
	s := sessions[0]
	id := s.Info.ID
	if id == "" {
		id = s.ID
	}
	if id == "" {
		return
	}
	title := s.Info.Title
	if title == "" {
		title = s.Title
	}
	dir := s.Info.Directory
	if dir == "" {
		dir = s.Directory
	}
	if dir == "" {
		dir = s.Path
	}
	c.mu.Lock()
	if _, ok := c.sessions[id]; !ok {
		c.sessions[id] = &TrackedSession{
			ID:        id,
			Title:     title,
			Directory: dir,
		}
	}
	c.mu.Unlock()
	c.seedSessionInfo(id)
}

// seedSessionInfo fetches a single session's info (title, directory) from the API
// and aggregates token/cost totals from its messages.
func (c *Client) seedSessionInfo(sessionID string) {
	req, err := http.NewRequestWithContext(c.ctx, "GET", fmt.Sprintf("%s/session/%s", c.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var item struct {
		Info struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Directory string `json:"directory"`
		} `json:"info"`
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return
	}
	title := item.Info.Title
	if title == "" {
		title = item.Title
	}
	dir := item.Info.Directory
	if dir == "" {
		dir = item.Directory
	}
	if dir == "" {
		dir = item.Path
	}
	c.mu.Lock()
	if ts, ok := c.sessions[sessionID]; ok {
		if title != "" {
			ts.Title = title
		}
		if dir != "" {
			ts.Directory = dir
		}
	}
	c.mu.Unlock()

	// Fetch all messages to aggregate token/cost totals
	c.seedSessionTokens(sessionID)
}

// seedSessionTokens fetches all messages for a session and sums token/cost totals,
// extracts the latest model ID and agent name, and records per-message snapshots
// for SSE delta tracking — all under a single lock acquisition.
func (c *Client) seedSessionTokens(sessionID string) {
	req, err := http.NewRequestWithContext(c.ctx, "GET", fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var messages []struct {
		Info struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Agent   string `json:"agent"`
			ModelID string `json:"modelID"`
			Tokens  struct {
				Input     int64 `json:"input"`
				Output    int64 `json:"output"`
				Reasoning int64 `json:"reasoning"`
				Cache     struct {
					Read  int64 `json:"read"`
					Write int64 `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
			Cost float64 `json:"cost"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return
	}

	// Build snapshots and aggregate totals without holding the lock.
	snapshots := make(map[string]msgTokenSnap, len(messages))
	var totalInput, totalOutput, totalCache int64
	var totalCost float64
	var lastModelID, lastAgent string
	var lastMsgInput, lastMsgCacheRead int64
	var msgCount int
	for _, m := range messages {
		if m.Info.Agent != "" {
			lastAgent = m.Info.Agent
		}
		if m.Info.ModelID != "" {
			lastModelID = m.Info.ModelID
		}
		if m.Info.Tokens.Input > 0 || m.Info.Tokens.Output > 0 || m.Info.Cost > 0 {
			totalInput += m.Info.Tokens.Input
			totalOutput += m.Info.Tokens.Output
			totalCache += m.Info.Tokens.Cache.Read
			totalCost += m.Info.Cost
			msgCount++
			if m.Info.ID != "" {
				snapshots[m.Info.ID] = msgTokenSnap{
					sessionID:    sessionID,
					inputTokens:  m.Info.Tokens.Input,
					outputTokens: m.Info.Tokens.Output,
					cacheRead:    m.Info.Tokens.Cache.Read,
					cost:         m.Info.Cost,
				}
			}
			// Track the most recent assistant message's individual token counts
			// for context usage calculation (last request = current context load).
			if m.Info.Role == "assistant" && m.Info.Tokens.Input > 0 {
				lastMsgInput = m.Info.Tokens.Input
				lastMsgCacheRead = m.Info.Tokens.Cache.Read
			}
		}
	}

	// Single lock acquisition to write everything atomically.
	c.mu.Lock()
	for id, snap := range snapshots {
		c.msgTokens[id] = snap
	}
	if ts, ok := c.sessions[sessionID]; ok {
		ts.InputTokens = totalInput
		ts.OutputTokens = totalOutput
		ts.CacheRead = totalCache
		ts.Cost = totalCost
		ts.MessageCount = msgCount
		ts.LastInputTokens = lastMsgInput
		ts.LastCacheRead = lastMsgCacheRead
		if lastModelID != "" {
			ts.ModelID = lastModelID
			if cw := c.modelLimits[lastModelID]; cw > 0 {
				ts.ContextWindow = cw
			}
		}
		if lastAgent != "" && ts.AgentName == "" {
			ts.AgentName = lastAgent
			c.sessionAgents[sessionID] = lastAgent
		}
	}
	c.mu.Unlock()
}

// FetchSessionOutput fetches all messages for a session, extracts text and tool
// usage from parts, and stores the result as OutputLines on the TrackedSession.
func (c *Client) FetchSessionOutput(sessionID string) {
	req, err := http.NewRequestWithContext(c.ctx, "GET", fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	// Read raw body for debugging and parsing
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var messages []struct {
		Info struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"info"`
		Parts json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(rawBody, &messages); err != nil {
		return
	}
	// Skip expensive output line extraction if message count unchanged and session is idle
	c.mu.Lock()
	prevCount := c.lastOutputCnt[sessionID]
	busy := c.busySessions[sessionID]
	c.mu.Unlock()
	if !busy && len(messages) == prevCount && prevCount > 0 {
		return
	}
	var allLines []OutputLine
	for _, m := range messages {
		if len(m.Parts) > 0 {
			lines := extractOutputLines(m.Parts, m.Info.Role)
			for _, l := range lines {
				allLines = append(allLines, OutputLine{Role: m.Info.Role, Text: l})
			}
		}
	}
	c.mu.Lock()
	if ts, ok := c.sessions[sessionID]; ok {
		ts.OutputLines = allLines
	}
	c.lastOutputCnt[sessionID] = len(messages)
	c.outputDirty[sessionID] = false
	c.mu.Unlock()
}

// FetchSessionOutputLines fetches all messages for a session and returns parsed
// OutputLines without storing them on the TrackedSession. This is designed to be
// called from a background goroutine (tea.Cmd) so the TUI event loop is not blocked.
func (c *Client) FetchSessionOutputLines(sessionID string) ([]OutputLine, bool) {
	// Fast path: if the session is idle and no new messages have arrived via SSE
	// since the last fetch, skip the HTTP round-trip entirely.
	c.mu.Lock()
	busy := c.busySessions[sessionID]
	dirty := c.outputDirty[sessionID]
	prevCount := c.lastOutputCnt[sessionID]
	c.mu.Unlock()
	if !busy && !dirty && prevCount > 0 {
		return nil, false
	}

	req, err := http.NewRequestWithContext(c.ctx, "GET", fmt.Sprintf("%s/session/%s/message", c.baseURL, sessionID), nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, false
	}
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false
	}
	var messages []struct {
		Info struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"info"`
		Parts json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(rawBody, &messages); err != nil {
		return nil, false
	}
	// Skip expensive output line extraction if message count unchanged and session is idle
	if !busy && len(messages) == prevCount && prevCount > 0 {
		return nil, false
	}
	var allLines []OutputLine
	for _, m := range messages {
		if len(m.Parts) > 0 {
			lines := extractOutputLines(m.Parts, m.Info.Role)
			for _, l := range lines {
				allLines = append(allLines, OutputLine{Role: m.Info.Role, Text: l})
			}
		}
	}
	c.mu.Lock()
	c.lastOutputCnt[sessionID] = len(messages)
	c.outputDirty[sessionID] = false
	c.mu.Unlock()
	return allLines, true
}

// seedAgentForSession fetches the most recent messages for a session and
// extracts the agent name from the latest assistant message.
func (c *Client) seedAgentForSession(sessionID string) {
	req, err := http.NewRequestWithContext(c.ctx, "GET", fmt.Sprintf("%s/session/%s/message?limit=5", c.baseURL, sessionID), nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return
	}
	var messages []struct {
		Info struct {
			Role  string `json:"role"`
			Agent string `json:"agent"`
		} `json:"info"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&messages); err != nil {
		return
	}
	// Walk messages in reverse to find the latest assistant message with an agent.
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Info.Role == "assistant" && m.Info.Agent != "" {
			c.mu.Lock()
			if c.sessionAgents[sessionID] == "" {
				c.sessionAgents[sessionID] = m.Info.Agent
			}
			if ts, ok := c.sessions[sessionID]; ok && ts.AgentName == "" {
				ts.AgentName = m.Info.Agent
			}
			c.mu.Unlock()
			return
		}
	}
}

func (c *Client) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.done)
		c.cancel()
	}
	c.mu.Unlock()
}

func (c *Client) streamEvents() {
	for {
		select {
		case <-c.done:
			return
		default:
		}
		c.connectSSE()
		select {
		case <-c.done:
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *Client) connectSSE() {
	req, err := http.NewRequestWithContext(c.ctx, "GET", c.baseURL+"/event", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.mu.Lock()
		if c.connected || c.disconnectedAt == nil {
			now := time.Now()
			c.disconnectedAt = &now
		}
		c.connected = false
		c.mu.Unlock()
		return
	}
	defer func() { _ = resp.Body.Close() }()

	c.mu.Lock()
	c.connected = true
	c.disconnectedAt = nil
	c.mu.Unlock()

	// Re-seed session state now that the SSE stream is active, so any events
	// that fire during or after the seed are caught by the scanner below.
	c.seedSessionStatus()

	if c.onEvent != nil {
		c.onEvent()
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	var dataBuf bytes.Buffer

	for scanner.Scan() {
		select {
		case <-c.done:
			return
		default:
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data:"))
		} else if line == "" && dataBuf.Len() > 0 {
			c.handleEvent(dataBuf.Bytes())
			dataBuf.Reset()
		}
	}

	c.mu.Lock()
	if c.connected || c.disconnectedAt == nil {
		now := time.Now()
		c.disconnectedAt = &now
	}
	c.connected = false
	c.mu.Unlock()
	if c.onEvent != nil {
		c.onEvent()
	}
}

// extractOutputLines renders message parts into display lines for the overview
// detail pane. Text parts are included directly; tool-use parts are shown as
// summary lines.
func extractOutputLines(partsJSON json.RawMessage, role string) []string {
	var parts []struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Tool    string `json:"tool"`
		Text    string `json:"text"`
		Content string `json:"content"`
		State   struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	if json.Unmarshal(partsJSON, &parts) != nil {
		return nil
	}
	var lines []string
	for _, p := range parts {
		switch p.Type {
		case "text":
			text := p.Text
			if text == "" {
				text = p.Content
			}
			if text != "" {
				lines = append(lines, strings.Split(text, "\n")...)
			}
		case "tool":
			name := p.Tool
			if name == "" {
				name = p.Name
			}
			if name != "" {
				status := ""
				if p.State.Status != "" {
					status = " (" + p.State.Status + ")"
				}
				lines = append(lines, "▸ "+name+status)
			}
		case "tool-use", "tool_use":
			if p.Name != "" {
				lines = append(lines, "▸ "+p.Name)
			}
		case "tool-result", "tool_result":
			lines = append(lines, "  ✓ done")
		}
	}
	return lines
}

// extractToolInfo finds the last tool_use part name and state from message parts JSON.
func extractToolInfo(partsJSON json.RawMessage) (name, state string) {
	var parts []struct {
		Type     string `json:"type"`
		ToolName string `json:"name"`
		Tool     string `json:"tool"`
		State    struct {
			Status string `json:"status"`
		} `json:"state"`
	}
	if json.Unmarshal(partsJSON, &parts) != nil {
		return "", ""
	}
	// Return the last tool_use or tool part name (most recent tool call)
	for i := len(parts) - 1; i >= 0; i-- {
		switch parts[i].Type {
		case "tool-use", "tool_use":
			return parts[i].ToolName, parts[i].State.Status
		case "tool":
			n := parts[i].Tool
			if n == "" {
				n = parts[i].ToolName
			}
			if n != "" {
				return n, parts[i].State.Status
			}
		}
	}
	return "", ""
}

// agentNameFromTitle extracts an agent name from a session title.
// Matches patterns like "Do something (@explore subagent)" or "(@build)".
func agentNameFromTitle(title string) string {
	if m := subagentTitleRe.FindStringSubmatch(title); len(m) > 1 {
		return m[1]
	}
	return ""
}

func (c *Client) handleEvent(data []byte) {
	var evt sseEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return
	}

	switch evt.Type {
	case "permission.updated", "permission.asked":
		var perm Permission
		if err := json.Unmarshal(evt.Properties, &perm); err != nil {
			return
		}
		c.mu.Lock()
		c.pending[perm.ID] = &perm
		c.mu.Unlock()
		if c.onEvent != nil {
			c.onEvent()
		}

	case "permission.replied":
		var reply struct {
			PermissionID string `json:"permissionID"`
			RequestID    string `json:"requestID"`
		}
		if err := json.Unmarshal(evt.Properties, &reply); err != nil {
			return
		}
		id := reply.PermissionID
		if id == "" {
			id = reply.RequestID
		}
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		if c.onEvent != nil {
			c.onEvent()
		}

	case "session.created", "session.updated":
		var props struct {
			Info struct {
				ID        string `json:"id"`
				ParentID  string `json:"parentID"`
				Title     string `json:"title"`
				Directory string `json:"directory"`
			} `json:"info"`
			Path      string `json:"path"`
			Directory string `json:"directory"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		dir := props.Info.Directory
		if dir == "" {
			dir = props.Directory
		}
		if dir == "" {
			dir = props.Path
		}
		// Child sessions have a parentID; their title often contains the agent name
		if props.Info.ParentID != "" && props.Info.Title != "" {
			name := agentNameFromTitle(props.Info.Title)
			if name != "" {
				c.mu.Lock()
				if c.sessionAgents[props.Info.ID] == "" {
					c.sessionAgents[props.Info.ID] = name
				}
				c.mu.Unlock()
			}
		}
		// Track root sessions (no parentID) with their title and directory
		if props.Info.ParentID == "" && props.Info.ID != "" {
			c.mu.Lock()
			if !isDefaultTitle(props.Info.Title) {
				c.isNewInstance = false
			}
			if ts, ok := c.sessions[props.Info.ID]; ok {
				if props.Info.Title != "" {
					ts.Title = props.Info.Title
				}
				if dir != "" {
					ts.Directory = dir
				}
			} else {
				c.sessions[props.Info.ID] = &TrackedSession{
					ID:        props.Info.ID,
					Title:     props.Info.Title,
					Directory: dir,
				}
			}
			c.mu.Unlock()
		}

	case "session.status":
		var props struct {
			SessionID string `json:"sessionID"`
			Status    struct {
				Type string `json:"type"`
			} `json:"status"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		busy := props.Status.Type == "busy"
		c.mu.Lock()
		_, known := c.sessions[props.SessionID]
		if busy {
			c.busySessions[props.SessionID] = true
		} else {
			delete(c.busySessions, props.SessionID)
		}
		if ts, ok := c.sessions[props.SessionID]; ok {
			ts.Busy = busy
			if busy {
				ts.LastActiveAt = time.Now()
			} else {
				// Session went idle — clear transient display fields.
				// WaitingForInput is managed by question.asked/replied events.
				ts.WaitingForInput = false
				ts.AgentName = ""
				ts.ToolName = ""
				ts.ToolState = ""
			}
		} else {
			ts := &TrackedSession{ID: props.SessionID, Busy: busy}
			if busy {
				ts.LastActiveAt = time.Now()
			}
			c.sessions[props.SessionID] = ts
		}
		c.mu.Unlock()
		// If this is a new session, fetch its info (title/directory/tokens/agent)
		if !known {
			c.seedSessionInfo(props.SessionID)
		} else if busy {
			// Known session gone busy again — re-seed agent if it was cleared
			c.mu.Lock()
			needsAgent := c.sessions[props.SessionID] != nil && c.sessions[props.SessionID].AgentName == ""
			c.mu.Unlock()
			if needsAgent {
				go c.seedAgentForSession(props.SessionID)
			}
		} else {
			// Session went idle — re-seed info to pick up updated title
			go c.seedSessionInfo(props.SessionID)
		}
		if c.onEvent != nil {
			c.onEvent()
		}

	case "message.updated":
		var props struct {
			Info struct {
				ID        string `json:"id"`
				Role      string `json:"role"`
				SessionID string `json:"sessionID"`
				Agent     string `json:"agent"`
				ModelID   string `json:"modelID"`
				Tokens    struct {
					Input     int64 `json:"input"`
					Output    int64 `json:"output"`
					Reasoning int64 `json:"reasoning"`
					Cache     struct {
						Read  int64 `json:"read"`
						Write int64 `json:"write"`
					} `json:"cache"`
				} `json:"tokens"`
				Cost  float64         `json:"cost"`
				Parts json.RawMessage `json:"parts"`
			} `json:"info"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		if props.Info.Role == "assistant" && props.Info.SessionID != "" {
			c.mu.Lock()
			if props.Info.Agent != "" {
				c.sessionAgents[props.Info.SessionID] = props.Info.Agent
				if ts, ok := c.sessions[props.Info.SessionID]; ok {
					ts.AgentName = props.Info.Agent
				}
			}
			// Update model ID
			if props.Info.ModelID != "" {
				if ts, ok := c.sessions[props.Info.SessionID]; ok {
					ts.ModelID = props.Info.ModelID
					if cw := c.modelLimits[props.Info.ModelID]; cw > 0 {
						ts.ContextWindow = cw
					}
				}
			}
			// Extract active tool name and state from parts
			if len(props.Info.Parts) > 0 {
				toolName, toolState := extractToolInfo(props.Info.Parts)
				if ts, ok := c.sessions[props.Info.SessionID]; ok {
					ts.ToolName = toolName
					ts.ToolState = toolState
					// Don't touch WaitingForInput here — question.asked/replied
					// events are the authoritative source for that state.
				}
			}
			// Update token/cost deltas
			if props.Info.ID != "" {
				prev, seen := c.msgTokens[props.Info.ID]
				if !seen {
					// New message — increment count
					if ts, ok := c.sessions[props.Info.SessionID]; ok {
						ts.MessageCount++
					}
					c.outputDirty[props.Info.SessionID] = true
				}
				dInput := props.Info.Tokens.Input - prev.inputTokens
				dOutput := props.Info.Tokens.Output - prev.outputTokens
				dCache := props.Info.Tokens.Cache.Read - prev.cacheRead
				dCost := props.Info.Cost - prev.cost
				c.msgTokens[props.Info.ID] = msgTokenSnap{
					sessionID:    props.Info.SessionID,
					inputTokens:  props.Info.Tokens.Input,
					outputTokens: props.Info.Tokens.Output,
					cacheRead:    props.Info.Tokens.Cache.Read,
					cost:         props.Info.Cost,
				}
				if ts, ok := c.sessions[props.Info.SessionID]; ok {
					ts.InputTokens += dInput
					ts.OutputTokens += dOutput
					ts.CacheRead += dCache
					ts.Cost += dCost
					// Keep last-message token counts current for context % calculation.
					if props.Info.Tokens.Input > 0 {
						ts.LastInputTokens = props.Info.Tokens.Input
						ts.LastCacheRead = props.Info.Tokens.Cache.Read
					}
				}
			}
			c.mu.Unlock()
		}

	case "question.asked":
		var props struct {
			SessionID string `json:"sessionID"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		if props.SessionID == "" {
			return
		}
		c.mu.Lock()
		if ts, ok := c.sessions[props.SessionID]; ok {
			ts.WaitingForInput = true
			ts.ToolName = "question"
			ts.ToolState = "running"
		}
		c.mu.Unlock()
		if c.onEvent != nil {
			c.onEvent()
		}

	case "question.replied", "question.rejected":
		var props struct {
			SessionID string `json:"sessionID"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		if props.SessionID == "" {
			return
		}
		c.mu.Lock()
		if ts, ok := c.sessions[props.SessionID]; ok {
			ts.WaitingForInput = false
		}
		c.mu.Unlock()
		if c.onEvent != nil {
			c.onEvent()
		}

	case "session.deleted":
		var props struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(evt.Properties, &props); err != nil {
			return
		}
		c.mu.Lock()
		delete(c.busySessions, props.ID)
		delete(c.sessionAgents, props.ID)
		delete(c.sessions, props.ID)
		delete(c.lastOutputCnt, props.ID)
		delete(c.outputDirty, props.ID)
		for msgID, snap := range c.msgTokens {
			if snap.sessionID == props.ID {
				delete(c.msgTokens, msgID)
			}
		}
		c.mu.Unlock()
	}
}
