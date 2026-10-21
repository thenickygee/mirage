package server

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// sameLocalServer reports whether two URLs point to the same local server
// (same port, both on loopback/unspecified addresses).
func sameLocalServer(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	portA := ua.Port()
	portB := ub.Port()
	if portA == "" || portB == "" || portA != portB {
		return false
	}
	return isLocalHost(ua.Hostname()) && isLocalHost(ub.Hostname())
}

func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	// Check if the IP belongs to any local network interface.
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// Pool manages connections to multiple session sources (OpenCode HTTP servers,
// Claude Code subprocesses, …) and presents a unified interface that merges
// data across all of them. Backends implement the Source interface; the Pool
// reaches into them only through Source methods, never via struct fields.
type Pool struct {
	mu            sync.Mutex
	sources       map[string]Source // source ID -> Source
	ignored       map[string]bool   // IDs removed by the user, skip during discovery
	onEvent       func()
	lastAddedID   string                                  // ID of the most recently added source
	stateVersion  atomic.Uint64                           // incremented on every state-changing event
	sourceFactory func(url string, onEvent func()) Source // constructs an OpenCode source from a URL
}

// NewPool creates a new connection pool. onEvent is called whenever any
// connected source receives an event.
func NewPool(onEvent func()) *Pool {
	return &Pool{
		sources: make(map[string]Source),
		ignored: make(map[string]bool),
		onEvent: onEvent,
	}
}

// SetSourceFactory registers the constructor used by Add to build an OpenCode
// HTTP source from a URL. This indirection keeps the server package from
// importing the opencode subpackage (avoids an import cycle). main.go sets it
// to opencode.NewSource before any Add/AddSource call.
func (p *Pool) SetSourceFactory(f func(url string, onEvent func()) Source) {
	p.mu.Lock()
	p.sourceFactory = f
	p.mu.Unlock()
}

// AddSource adds an already-constructed source to the pool and connects it.
// This is the generic entry point used by non-HTTP sources (e.g. Claude) and
// is also called by Add after the factory constructs an HTTP source. If a
// source with the same ID — or one on the same local server — already exists
// (or was explicitly removed and is in the ignored set), it is a no-op.
func (p *Pool) AddSource(src Source) error {
	p.mu.Lock()
	id := src.ID()
	if p.ignored[id] {
		p.mu.Unlock()
		return nil
	}
	if _, ok := p.sources[id]; ok {
		p.mu.Unlock()
		return nil
	}
	for existingID := range p.sources {
		if sameLocalServer(id, existingID) {
			p.mu.Unlock()
			return nil
		}
	}
	for ignoredID := range p.ignored {
		if sameLocalServer(id, ignoredID) {
			p.mu.Unlock()
			return nil
		}
	}
	p.sources[id] = src
	p.lastAddedID = id
	p.mu.Unlock()
	if err := src.Connect(); err != nil {
		p.mu.Lock()
		delete(p.sources, id)
		p.mu.Unlock()
		return err
	}
	return nil
}

// Add connects to an OpenCode server at the given URL and adds it to the pool.
// If a source for that URL already exists it is a no-op.
func (p *Pool) Add(baseURL string) error {
	p.mu.Lock()
	if p.ignored[baseURL] {
		p.mu.Unlock()
		return nil
	}
	if _, ok := p.sources[baseURL]; ok {
		p.mu.Unlock()
		return nil
	}
	// De-duplicate: if an existing source is on the same local port, skip.
	for existingID := range p.sources {
		if sameLocalServer(baseURL, existingID) {
			p.mu.Unlock()
			return nil
		}
	}
	// Also check ignored IDs for same-local-server matches.
	for ignoredID := range p.ignored {
		if sameLocalServer(baseURL, ignoredID) {
			p.mu.Unlock()
			return nil
		}
	}
	factory := p.sourceFactory
	p.mu.Unlock()
	if factory == nil {
		return fmt.Errorf("no source factory registered (call SetSourceFactory before Add)")
	}
	onEvent := p.onEvent
	wrappedOnEvent := func() {
		p.bumpVersion()
		if onEvent != nil {
			onEvent()
		}
	}
	src := factory(baseURL, wrappedOnEvent)
	return p.AddSource(src)
}

// Remove disconnects and removes the source with the given ID.
func (p *Pool) Remove(id string) {
	p.mu.Lock()
	p.ignored[id] = true
	src, ok := p.sources[id]
	if ok {
		delete(p.sources, id)
	}
	p.mu.Unlock()
	if ok {
		src.Close()
	}
}

// URLs returns the IDs of all OpenCode HTTP sources currently in the pool.
// (Non-HTTP source IDs are filtered out for backwards compatibility with the
// mDNS/HTTP discovery callers that only expect URLs.)
func (p *Pool) URLs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.sources))
	for id, src := range p.sources {
		if src.Kind() == "opencode" {
			out = append(out, id)
		}
	}
	return out
}

// LastAddedURL returns the ID of the most recently added source.
func (p *Pool) LastAddedURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAddedID
}

// StateVersion returns a monotonically increasing counter that increments each
// time any client's state changes (SSE event, connect, disconnect). Callers can
// compare against a stored value to skip work when nothing has changed.
func (p *Pool) StateVersion() uint64 {
	return p.stateVersion.Load()
}

// bumpVersion atomically increments the state version. Safe to call from any
// goroutine without holding p.mu (avoids lock-ordering issues with Client.mu).
func (p *Pool) bumpVersion() {
	p.stateVersion.Add(1)
}

// NotifyChange bumps the pool's state version and fires the onEvent callback
// (which typically sends a UI message to trigger a redraw). Sources whose
// onEvent isn't pre-wrapped by Add() — i.e. anything added via AddSource, such
// as the Claude Code source — should pass p.NotifyChange as their onEvent so
// the overview list refreshes on state transitions the same way HTTP sources
// do. Safe to call from any goroutine.
func (p *Pool) NotifyChange() {
	p.bumpVersion()
	if p.onEvent != nil {
		p.onEvent()
	}
}

// Connected returns true if at least one source is connected.
func (p *Pool) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sources {
		if s.Connected() {
			return true
		}
	}
	return false
}

// ConnectedCount returns the number of currently connected sources.
func (p *Pool) ConnectedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.sources {
		if s.Connected() {
			n++
		}
	}
	return n
}

// HasActiveAgents returns true if any source has a busy session.
func (p *Pool) HasActiveAgents() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.sources {
		if s.HasActiveAgents() {
			return true
		}
	}
	return false
}

// ActiveAgents merges active agent names across all sources.
func (p *Pool) ActiveAgents() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := make(map[string]bool)
	for _, s := range p.sources {
		for k, v := range s.ActiveAgents() {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ActiveSessions merges sessionID -> agentName across all sources.
func (p *Pool) ActiveSessions() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := make(map[string]string)
	for _, s := range p.sources {
		for k, v := range s.ActiveSessions() {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ActiveState combines HasActiveAgents, ActiveSessions, and ActiveAgents into
// a single mutex acquisition to avoid 3 separate locks on the hot tick path.
func (p *Pool) ActiveState() (hasActive bool, sessions map[string]string, agents map[string]bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sessions = make(map[string]string)
	agents = make(map[string]bool)
	for _, s := range p.sources {
		if !hasActive && s.HasActiveAgents() {
			hasActive = true
		}
		for k, v := range s.ActiveSessions() {
			sessions[k] = v
		}
		for k, v := range s.ActiveAgents() {
			agents[k] = v
		}
	}
	if len(sessions) == 0 {
		sessions = nil
	}
	if len(agents) == 0 {
		agents = nil
	}
	return hasActive, sessions, agents
}

// ConnectedStatus combines Connected and ConnectedCount into a single lock.
func (p *Pool) ConnectedStatus() (bool, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	connected := false
	for _, s := range p.sources {
		if s.Connected() {
			connected = true
			n++
		}
	}
	return connected, n
}

// PendingCount returns the total number of pending permissions across all sources.
func (p *Pool) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.sources {
		n += s.PendingCount()
	}
	return n
}

// Pending returns all pending permissions across all sources.
func (p *Pool) Pending() []*Permission {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Permission
	for _, s := range p.sources {
		out = append(out, s.Pending()...)
	}
	return out
}

// PendingForSession returns pending permissions for a specific session across all sources.
func (p *Pool) PendingForSession(sessionID string) []*Permission {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Permission
	for _, s := range p.sources {
		out = append(out, s.PendingForSession(sessionID)...)
	}
	return out
}

// Respond sends a permission response to whichever source holds the given permission.
func (p *Pool) Respond(sessionID, permissionID, response string) error {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()

	for _, s := range sources {
		if s.HasPermission(permissionID) {
			return s.Respond(sessionID, permissionID, response)
		}
	}
	// Permission not found in any source — try the first connected one as fallback.
	for _, s := range sources {
		if s.Connected() {
			return s.Respond(sessionID, permissionID, response)
		}
	}
	return nil
}

// ConnectedServerInfo holds the status of a single connected source instance.
type ConnectedServerInfo struct {
	URL            string // source ID (baseURL for opencode, "claude:<pid>" for claude)
	Kind           string // "opencode" | "claude"
	Connected      bool
	DisconnectedAt *time.Time        // non-nil when disconnected, cleared on reconnect
	IsNewInstance  bool              // true until first session.created event is received
	ActiveSessions map[string]string // sessionID -> agentName
	Sessions       []*TrackedSession
	PendingCount   int
}

// ConnectedServers returns a snapshot of each source in the pool with its connection
// state and active sessions. The slice is sorted by URL for stable display ordering.
func (p *Pool) ConnectedServers() []ConnectedServerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ConnectedServerInfo, 0, len(p.sources))
	for id, s := range p.sources {
		sessions := s.TrackedSessions()
		// Hide the Claude source until it owns at least one session so the
		// overview stays clean for OpenCode-only users (the source is
		// auto-registered when `claude` is on $PATH + a key is available).
		if s.Kind() == "claude" && len(sessions) == 0 {
			continue
		}
		out = append(out, ConnectedServerInfo{
			URL:            id,
			Kind:           s.Kind(),
			Connected:      s.Connected(),
			DisconnectedAt: s.DisconnectedAt(),
			IsNewInstance:  s.IsNewInstance(),
			ActiveSessions: s.ActiveSessions(),
			Sessions:       sessions,
			PendingCount:   s.PendingCount(),
		})
	}
	// Sort by URL for stable ordering
	sort.Slice(out, func(i, j int) bool {
		return out[i].URL < out[j].URL
	})
	return out
}

// SendMessage sends a text message to a session via whichever source owns it.
func (p *Pool) SendMessage(sessionID, text string) error {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()
	for _, s := range sources {
		if s.OwnsSession(sessionID) {
			return s.SendMessage(sessionID, text)
		}
	}
	// Fallback to first connected source.
	for _, s := range sources {
		if s.Connected() {
			return s.SendMessage(sessionID, text)
		}
	}
	return nil
}

// Close disconnects all sources.
func (p *Pool) Close() {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.sources = make(map[string]Source)
	p.mu.Unlock()
	for _, s := range sources {
		s.Close()
	}
}

// FetchSessionOutput fetches message content for a session from whichever
// source owns it, populating TrackedSession.OutputLines.
func (p *Pool) FetchSessionOutput(sessionID string) {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()
	for _, s := range sources {
		if s.OwnsSession(sessionID) {
			s.FetchSessionOutput(sessionID)
			return
		}
	}
}

// FetchSessionOutputLines fetches message content for a session and returns
// the parsed OutputLines without storing them. Returns (nil, false) if no
// update was needed (unchanged message count on idle session).
func (p *Pool) FetchSessionOutputLines(sessionID string) ([]OutputLine, bool) {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()
	for _, s := range sources {
		if s.OwnsSession(sessionID) {
			return s.FetchSessionOutputLines(sessionID)
		}
	}
	return nil, false
}

// SetSessionOutputLines stores pre-fetched output lines on a tracked session.
func (p *Pool) SetSessionOutputLines(sessionID string, lines []OutputLine) {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()
	for _, s := range sources {
		if s.OwnsSession(sessionID) {
			s.SetSessionOutputLines(sessionID, lines)
			return
		}
	}
}

// ClaudeLauncher is the interface a source implements to support launching
// fresh Claude Code sessions from the UI. The Claude source implements it;
// OpenCode sources do not (the launcher is a no-op for them).
type ClaudeLauncher interface {
	Launch(prompt, dir string) error
}

// LaunchClaudeSession starts a fresh Claude Code session with the given
// initial prompt and working directory (dir may be empty to use the source's
// configured default). It routes to the first source that implements
// ClaudeLauncher. Returns an error if no Claude source is registered.
func (p *Pool) LaunchClaudeSession(prompt, dir string) error {
	p.mu.Lock()
	sources := make([]Source, 0, len(p.sources))
	for _, s := range p.sources {
		sources = append(sources, s)
	}
	p.mu.Unlock()
	for _, s := range sources {
		if cl, ok := s.(ClaudeLauncher); ok {
			return cl.Launch(prompt, dir)
		}
	}
	return fmt.Errorf("no Claude source available (start mirage with -claude)")
}
