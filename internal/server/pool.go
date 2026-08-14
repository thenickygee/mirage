
package server

import (
	"net"
	"net/url"
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

// Pool manages connections to multiple OpenCode server instances and presents
// a unified interface that merges data across all of them.
type Pool struct {
	mu           sync.Mutex
	clients      map[string]*Client // baseURL -> Client
	ignored      map[string]bool    // URLs removed by the user, skip during discovery
	onEvent      func()
	lastAddedURL string        // URL of the most recently added client
	stateVersion atomic.Uint64 // incremented on every state-changing event
}

// NewPool creates a new connection pool. onEvent is called whenever any
// connected client receives an event.
func NewPool(onEvent func()) *Pool {
	return &Pool{
		clients: make(map[string]*Client),
		ignored: make(map[string]bool),
		onEvent: onEvent,
	}
}

// Add connects to a server at the given URL and adds it to the pool.
// If a client for that URL already exists it is a no-op.
func (p *Pool) Add(baseURL string) error {
	p.mu.Lock()
	if p.ignored[baseURL] {
		p.mu.Unlock()
		return nil
	}
	if _, ok := p.clients[baseURL]; ok {
		p.mu.Unlock()
		return nil
	}
	// De-duplicate: if an existing client is on the same local port, skip.
	for existingURL := range p.clients {
		if sameLocalServer(baseURL, existingURL) {
			p.mu.Unlock()
			return nil
		}
	}
	// Also check ignored URLs for same-local-server matches.
	for ignoredURL := range p.ignored {
		if sameLocalServer(baseURL, ignoredURL) {
			p.mu.Unlock()
			return nil
		}
	}
	onEvent := p.onEvent
	wrappedOnEvent := func() {
		p.bumpVersion()
		if onEvent != nil {
			onEvent()
		}
	}
	c := New(baseURL, wrappedOnEvent)
	p.clients[baseURL] = c
	p.lastAddedURL = baseURL
	p.mu.Unlock()

	return c.Connect()
}

// Remove disconnects and removes the client for baseURL.
func (p *Pool) Remove(baseURL string) {
	p.mu.Lock()
	p.ignored[baseURL] = true
	c, ok := p.clients[baseURL]
	if ok {
		delete(p.clients, baseURL)
	}
	p.mu.Unlock()
	if ok {
		c.Close()
	}
}

// URLs returns the base URLs of all clients currently in the pool.
func (p *Pool) URLs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.clients))
	for u := range p.clients {
		out = append(out, u)
	}
	return out
}

// LastAddedURL returns the URL of the most recently added client.
func (p *Pool) LastAddedURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAddedURL
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

// Connected returns true if at least one client is connected.
func (p *Pool) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		if c.Connected() {
			return true
		}
	}
	return false
}

// ConnectedCount returns the number of currently connected clients.
func (p *Pool) ConnectedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.clients {
		if c.Connected() {
			n++
		}
	}
	return n
}

// HasActiveAgents returns true if any client has a busy session.
func (p *Pool) HasActiveAgents() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.clients {
		if c.HasActiveAgents() {
			return true
		}
	}
	return false
}

// ActiveAgents merges active agent names across all clients.
func (p *Pool) ActiveAgents() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := make(map[string]bool)
	for _, c := range p.clients {
		for k, v := range c.ActiveAgents() {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// ActiveSessions merges sessionID -> agentName across all clients.
func (p *Pool) ActiveSessions() map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	merged := make(map[string]string)
	for _, c := range p.clients {
		for k, v := range c.ActiveSessions() {
			merged[k] = v
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// PendingCount returns the total number of pending permissions across all clients.
func (p *Pool) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.clients {
		n += c.PendingCount()
	}
	return n
}

// Pending returns all pending permissions across all clients.
func (p *Pool) Pending() []*Permission {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Permission
	for _, c := range p.clients {
		out = append(out, c.Pending()...)
	}
	return out
}

// PendingForSession returns pending permissions for a specific session across all clients.
func (p *Pool) PendingForSession(sessionID string) []*Permission {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []*Permission
	for _, c := range p.clients {
		out = append(out, c.PendingForSession(sessionID)...)
	}
	return out
}

// Respond sends a permission response to whichever client holds the given permission.
func (p *Pool) Respond(sessionID, permissionID, response string) error {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()

	for _, c := range clients {
		c.mu.Lock()
		_, hasPerm := c.pending[permissionID]
		c.mu.Unlock()
		if hasPerm {
			return c.Respond(sessionID, permissionID, response)
		}
	}
	// Permission not found in any client — try the first connected one as fallback
	for _, c := range clients {
		if c.Connected() {
			return c.Respond(sessionID, permissionID, response)
		}
	}
	return nil
}

// ConnectedServerInfo holds the status of a single connected server instance.
type ConnectedServerInfo struct {
	URL            string
	Connected      bool
	DisconnectedAt *time.Time        // non-nil when disconnected, cleared on reconnect
	IsNewInstance  bool              // true until first session.created event is received
	ActiveSessions map[string]string // sessionID -> agentName
	Sessions       []*TrackedSession
	PendingCount   int
}

// ConnectedServers returns a snapshot of each server in the pool with its connection
// state and active sessions. The slice is sorted by URL for stable display ordering.
func (p *Pool) ConnectedServers() []ConnectedServerInfo {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ConnectedServerInfo, 0, len(p.clients))
	for url, c := range p.clients {
		out = append(out, ConnectedServerInfo{
			URL:            url,
			Connected:      c.Connected(),
			DisconnectedAt: c.DisconnectedAt(),
			IsNewInstance:  c.IsNewInstance(),
			ActiveSessions: c.ActiveSessions(),
			Sessions:       c.TrackedSessions(),
			PendingCount:   c.PendingCount(),
		})
	}
	// Sort by URL for stable ordering
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].URL < out[j-1].URL; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// SendMessage sends a text message to a session via whichever client owns it.
func (p *Pool) SendMessage(sessionID, text string) error {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()
	for _, c := range clients {
		c.mu.Lock()
		_, owns := c.sessions[sessionID]
		c.mu.Unlock()
		if owns {
			return c.SendMessage(sessionID, text)
		}
	}
	// Fallback to first connected client
	for _, c := range clients {
		if c.Connected() {
			return c.SendMessage(sessionID, text)
		}
	}
	return nil
}

// Close disconnects all clients.
func (p *Pool) Close() {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.clients = make(map[string]*Client)
	p.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

// FetchSessionOutput fetches message content for a session from whichever
// client owns it, populating TrackedSession.OutputLines.
func (p *Pool) FetchSessionOutput(sessionID string) {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()
	for _, c := range clients {
		c.mu.Lock()
		_, owns := c.sessions[sessionID]
		c.mu.Unlock()
		if owns {
			c.FetchSessionOutput(sessionID)
			return
		}
	}
}

// FetchSessionOutputLines fetches message content for a session and returns
// the parsed OutputLines without storing them. Returns (nil, false) if no
// update was needed (unchanged message count on idle session).
func (p *Pool) FetchSessionOutputLines(sessionID string) ([]OutputLine, bool) {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()
	for _, c := range clients {
		c.mu.Lock()
		_, owns := c.sessions[sessionID]
		c.mu.Unlock()
		if owns {
			return c.FetchSessionOutputLines(sessionID)
		}
	}
	return nil, false
}

// SetSessionOutputLines stores pre-fetched output lines on a tracked session.
func (p *Pool) SetSessionOutputLines(sessionID string, lines []OutputLine) {
	p.mu.Lock()
	clients := make([]*Client, 0, len(p.clients))
	for _, c := range p.clients {
		clients = append(clients, c)
	}
	p.mu.Unlock()
	for _, c := range clients {
		c.mu.Lock()
		if ts, ok := c.sessions[sessionID]; ok {
			ts.OutputLines = lines
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}
