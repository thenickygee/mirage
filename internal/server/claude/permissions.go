package claude

import (
	"encoding/json"
	"sort"
	"time"
)

const rejectMessage = "Rejected from Mirage"

// pendingRequest is the claude-side context for a Permission shown in the UI:
// which subprocess to answer and the data needed to build the response.
type pendingRequest struct {
	proc        *sessionProc
	input       json.RawMessage
	suggestions json.RawMessage
}

// handleControlRequest turns a can_use_tool control request into a pending
// Permission for the UI. Unknown request subtypes are answered with an error so
// claude never blocks waiting on a reply Mirage can't give.
func (s *Source) handleControlRequest(evt streamEvent, sessionID string, proc *sessionProc) {
	if evt.RequestID == "" || proc == nil {
		return
	}
	var req controlRequest
	if err := json.Unmarshal(evt.Request, &req); err != nil || req.Subtype != "can_use_tool" {
		_ = proc.send(controlResponse(evt.RequestID, "unsupported control request: "+req.Subtype, nil))
		return
	}

	summary := summarizeInput(req.Input)
	perm := &Permission{
		ID:        evt.RequestID,
		Type:      firstNonEmpty(req.DisplayName, req.ToolName),
		SessionID: sessionID,
		MessageID: req.ToolUseID,
		CallID:    req.ToolUseID,
		Title:     firstNonEmpty(req.Description, summary),
	}
	perm.Time.Created = time.Now().UnixMilli()
	if summary != "" {
		perm.Pattern, _ = json.Marshal(summary)
	}
	if len(req.Input) > 0 {
		var input map[string]any
		if json.Unmarshal(req.Input, &input) == nil {
			perm.Metadata = map[string]any{"input": input}
		}
	}

	s.mu.Lock()
	s.pending[perm.ID] = perm
	s.pendingReqs[perm.ID] = &pendingRequest{proc: proc, input: req.Input, suggestions: req.PermissionSuggestions}
	if ts, ok := s.sessions[sessionID]; ok {
		ts.ToolName = req.ToolName
		ts.ToolState = "pending"
		ts.LastActiveAt = time.Now()
	}
	s.mu.Unlock()
	s.fireEvent()
}

// handleControlCancel drops a pending permission claude no longer needs
// answered (e.g. the tool call was aborted).
func (s *Source) handleControlCancel(evt streamEvent) {
	s.mu.Lock()
	delete(s.pending, evt.RequestID)
	delete(s.pendingReqs, evt.RequestID)
	s.mu.Unlock()
	s.fireEvent()
}

// dropPendingLocked discards every pending permission owned by proc. Callers
// must hold s.mu.
func (s *Source) dropPendingLocked(proc *sessionProc) {
	for id, req := range s.pendingReqs {
		if req.proc == proc {
			delete(s.pending, id)
			delete(s.pendingReqs, id)
		}
	}
}

// permissionPayload builds the can_use_tool response for a UI answer:
// "once" allows this call, "always" also applies claude's suggested permission
// update (so the rule persists), anything else denies.
func permissionPayload(response string, req *pendingRequest) map[string]any {
	if response != "once" && response != "always" {
		return map[string]any{"behavior": "deny", "message": rejectMessage}
	}
	input := req.input
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	payload := map[string]any{"behavior": "allow", "updatedInput": input}
	if response == "always" && len(req.suggestions) > 0 {
		payload["updatedPermissions"] = req.suggestions
	}
	return payload
}

// summarizeInput picks the human-meaningful field of a tool input (the
// command, path, pattern or URL) for display, falling back to compact JSON.
func summarizeInput(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "notebook_path", "path", "pattern", "url", "query"} {
		if v, ok := m[key].(string); ok && v != "" {
			return v
		}
	}
	return truncate(string(input), 200)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- server.Source permission methods ---

func (s *Source) Pending() []*Permission {
	return s.pendingMatching(func(*Permission) bool { return true })
}

func (s *Source) PendingForSession(sessionID string) []*Permission {
	return s.pendingMatching(func(p *Permission) bool { return p.SessionID == sessionID })
}

func (s *Source) pendingMatching(keep func(*Permission) bool) []*Permission {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Permission, 0, len(s.pending))
	for _, p := range s.pending {
		if keep(p) {
			cp := *p
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Created < out[j].Time.Created })
	return out
}

func (s *Source) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.pending)
}

func (s *Source) HasPermission(permID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[permID]
	return ok
}

// Respond answers a pending permission. A permission that is already gone
// (answered elsewhere, or its process exited) is not an error.
func (s *Source) Respond(sessionID, permissionID, response string) error {
	s.mu.Lock()
	req, ok := s.pendingReqs[permissionID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.pending, permissionID)
	delete(s.pendingReqs, permissionID)
	if ts, ok := s.sessions[sessionID]; ok && ts.ToolState == "pending" {
		ts.ToolState = "running"
	}
	s.mu.Unlock()

	err := req.proc.send(controlResponse(permissionID, "", permissionPayload(response, req)))
	s.fireEvent()
	return err
}
