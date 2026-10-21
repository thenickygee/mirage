package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thenickygee/mirage/internal/server"
)

// handleLine parses one NDJSON line from a claude subprocess's stdout and
// applies the corresponding mutation to that subprocess's session state.
// sessionID is the ID the launcher assigned to this subprocess; it's used to
// route events to the correct TrackedSession so multiple concurrent claude
// runs don't cross-contaminate. Unknown event types/subtypes are ignored
// gracefully so schema drift doesn't break Mirage. All mutations happen under
// s.mu; onEvent is fired after each meaningful mutation so the Pool bumps its
// state version and the UI redraws. Returns the session ID the line resolved
// to (re-keyed by system/init if claude returns a different session_id than the
// one we launched with).
func (s *Source) handleLine(line []byte, sessionID string) string {
	var evt streamEvent
	if err := json.Unmarshal(line, &evt); err != nil {
		// Non-JSON lines (e.g. stray stderr bleeding into stdout) are ignored.
		return sessionID
	}

	switch evt.Type {
	case "system":
		return s.handleSystem(evt, sessionID)
	case "assistant":
		s.handleAssistant(evt, sessionID)
	case "user":
		s.handleUser(evt, sessionID)
	case "stream_event":
		s.handleStreamEvent(evt, sessionID)
	case "result":
		s.handleResult(evt, sessionID)
	default:
		// Unknown top-level type: ignore (schema drift tolerance).
	}
	return sessionID
}

// handleSystem dispatches system events by subtype (init, api_retry, ...).
func (s *Source) handleSystem(evt streamEvent, sessionID string) string {
	switch evt.Subtype {
	case "init":
		return s.handleInit(evt, sessionID)
	case "api_retry":
		s.handleAPIRetry(evt, sessionID)
	default:
		// Other system subtypes (e.g. plugin_install) are ignored for now.
	}
	return sessionID
}

// handleInit seeds the session with the model ID, context window, working
// directory, and the authoritative session_id returned by claude. If the
// returned session_id differs from the one we launched with (shouldn't happen
// under --session-id / --resume), the tracked session is re-keyed and the new
// ID is returned so the scanner routes subsequent events correctly.
func (s *Source) handleInit(evt streamEvent, sessionID string) string {
	resolvedID := sessionID
	if evt.SessionID != "" {
		resolvedID = evt.SessionID
	}
	s.mu.Lock()
	ts, ok := s.sessions[resolvedID]
	if !ok && resolvedID != sessionID {
		// Re-key the pre-launched placeholder session to the real ID.
		ts, ok = s.sessions[sessionID]
		if ok {
			delete(s.sessions, sessionID)
			delete(s.busySessions, sessionID)
			delete(s.streamingIdx, sessionID)
			ts.ID = resolvedID
			s.sessions[resolvedID] = ts
		}
	}
	if !ok {
		ts = &TrackedSession{ID: resolvedID}
		s.sessions[resolvedID] = ts
	}
	ts.Busy = true
	ts.LastActiveAt = time.Now()
	if evt.Model != "" {
		ts.ModelID = evt.Model
		ts.ContextWindow = contextWindowFor(evt.Model)
	}
	if evt.Cwd != "" {
		ts.Directory = evt.Cwd
	}
	if ts.Title == "" {
		ts.Title = "Claude Session"
	}
	s.busySessions[resolvedID] = true
	if s.pendingID == sessionID || s.pendingID == resolvedID {
		s.pendingID = ""
	}
	s.mu.Unlock()
	s.fireEvent()
	return resolvedID
}

// handleAPIRetry records a transient retry state on the session. The status
// row surfaces it as "retrying" (see overview.go); cleared on the next
// assistant block.
func (s *Source) handleAPIRetry(evt streamEvent, sessionID string) {
	s.mu.Lock()
	if ts, ok := s.sessions[sessionID]; ok && ts.Busy {
		ts.ToolName = "api_retry"
		ts.ToolState = "retrying"
	}
	s.mu.Unlock()
	s.fireEvent()
}

// handleAssistant appends assistant text/tool_use blocks as OutputLines and
// records tool_use IDs for later tool_result matching.
func (s *Source) handleAssistant(evt streamEvent, sessionID string) {
	var msg messageEnvelope
	if err := json.Unmarshal(evt.Message, &msg); err != nil {
		return
	}
	s.mu.Lock()
	ts, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	// Clear transient api_retry state on the first new assistant block.
	if ts.ToolState == "retrying" {
		ts.ToolName = ""
		ts.ToolState = ""
	}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) == "" {
				continue
			}
			// If stream_event deltas already built a live line for this
			// message, the final assistant block is authoritative —
			// replace the streaming line's text instead of appending a
			// duplicate. This avoids flicker while keeping the final
			// value exact (deltas could be dropped/truncated on error).
			if idx, ok := s.streamingIdx[sessionID]; ok && idx >= 0 && idx < len(ts.OutputLines) {
				ts.OutputLines[idx].Text = block.Text
				delete(s.streamingIdx, sessionID)
			} else {
				for _, line := range strings.Split(block.Text, "\n") {
					ts.OutputLines = append(ts.OutputLines, server.OutputLine{
						Role: "assistant",
						Text: line,
					})
				}
			}
		case "thinking":
			// Thinking blocks are not surfaced (the plan only lists text
			// and tool_use). Kept as a no-op for future polish.
		case "tool_use":
			if block.Name == "" {
				continue
			}
			ts.ToolName = block.Name
			ts.ToolState = "running"
			ts.LastActiveAt = time.Now()
			ts.OutputLines = append(ts.OutputLines, server.OutputLine{
				Role: "assistant",
				Text: "▸ " + block.Name,
			})
			if block.ID != "" {
				s.toolUseNames[block.ID] = block.Name
			}
		}
	}
	s.outputDirty[sessionID] = true
	s.mu.Unlock()
	s.fireEvent()
}

// handleUser matches tool_result blocks to their tool_use IDs and appends a
// completion line.
func (s *Source) handleUser(evt streamEvent, sessionID string) {
	var msg messageEnvelope
	if err := json.Unmarshal(evt.Message, &msg); err != nil {
		return
	}
	s.mu.Lock()
	ts, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}
		mark := "  ✓ done"
		if block.IsError {
			mark = "  ✗ error"
		}
		ts.OutputLines = append(ts.OutputLines, server.OutputLine{
			Role: "user",
			Text: mark,
		})
		ts.ToolState = "completed"
		ts.LastActiveAt = time.Now()
	}
	s.outputDirty[sessionID] = true
	s.mu.Unlock()
	s.fireEvent()
}

// handleStreamEvent drives live text-delta streaming: it appends partial
// assistant text to an in-progress OutputLine as it arrives, so the user sees
// the message build up in real time instead of waiting for the full assistant
// block. The final assistant block (handleAssistant) reconciles by replacing
// the streaming line with the authoritative text.
func (s *Source) handleStreamEvent(evt streamEvent, sessionID string) {
	var env streamEventEnvelope
	if err := json.Unmarshal(evt.Event, &env); err != nil {
		return
	}
	switch env.Type {
	case "content_block_start":
		var blk contentBlockStart
		if len(env.ContentBlock) > 0 {
			if err := json.Unmarshal(env.ContentBlock, &blk); err != nil {
				return
			}
		}
		if blk.Type != "text" {
			return
		}
		s.mu.Lock()
		ts, ok := s.sessions[sessionID]
		if !ok || !ts.Busy {
			s.mu.Unlock()
			return
		}
		ts.OutputLines = append(ts.OutputLines, server.OutputLine{
			Role: "assistant",
			Text: blk.Text,
		})
		s.streamingIdx[sessionID] = len(ts.OutputLines) - 1
		s.outputDirty[sessionID] = true
		s.mu.Unlock()
		s.fireEvent()
	case "content_block_delta":
		var d streamDelta
		if len(env.Delta) > 0 {
			if err := json.Unmarshal(env.Delta, &d); err != nil {
				return
			}
		}
		if d.Type != "text_delta" || d.Text == "" {
			return
		}
		s.mu.Lock()
		ts, ok := s.sessions[sessionID]
		if !ok || !ts.Busy {
			s.mu.Unlock()
			return
		}
		idx, ok := s.streamingIdx[sessionID]
		if !ok || idx < 0 || idx >= len(ts.OutputLines) {
			s.mu.Unlock()
			return
		}
		ts.OutputLines[idx].Text += d.Text
		s.outputDirty[sessionID] = true
		s.mu.Unlock()
		s.fireEvent()
	case "content_block_stop", "message_stop":
		// Leave the streaming index in place until the assistant block
		// arrives to reconcile; just stop appending deltas.
	}
}

// handleResult finalizes the session: clears Busy, records token/cost totals
// from the authoritative result event, and appends an error line on failure.
func (s *Source) handleResult(evt streamEvent, sessionID string) {
	s.mu.Lock()
	ts, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return
	}
	ts.Busy = false
	ts.WaitingForInput = false
	ts.ToolState = ""
	if evt.Subtype != "success" && evt.Subtype != "" {
		msg := evt.Result
		if msg == "" && len(evt.Errors) > 0 {
			msg = strings.Join(evt.Errors, "; ")
		}
		if msg == "" {
			msg = fmt.Sprintf("claude run ended: %s", evt.Subtype)
		}
		ts.OutputLines = append(ts.OutputLines, server.OutputLine{
			Role: "assistant",
			Text: "✗ " + msg,
		})
	}
	if evt.Usage != nil {
		ts.InputTokens = evt.Usage.InputTokens
		ts.OutputTokens = evt.Usage.OutputTokens
		ts.CacheRead = evt.Usage.CacheReadInputTokens
		// Context usage uses the last request's input + cache read.
		ts.LastInputTokens = evt.Usage.InputTokens
		ts.LastCacheRead = evt.Usage.CacheReadInputTokens
	}
	if evt.TotalCostUSD > 0 {
		ts.Cost = evt.TotalCostUSD
	}
	ts.MessageCount++
	ts.LastActiveAt = time.Now()
	delete(s.busySessions, sessionID)
	delete(s.streamingIdx, sessionID)
	s.outputDirty[sessionID] = true
	s.mu.Unlock()
	s.fireEvent()
}

// fireEvent is the post-mutation callback into the Pool's onEvent hook.
func (s *Source) fireEvent() {
	if s.onEvent != nil {
		s.onEvent()
	}
}
