package claude

import "encoding/json"

// streamEvent is the top-level envelope for one line of Claude Code's
// --output-format stream-json NDJSON output. Each line carries a `type` field;
// `system` and `result` events further discriminate via `subtype`.
//
// Unknown type/subtype values are ignored gracefully (parse.go logs at debug
// and never errors) so schema drift in newer Claude Code builds doesn't break
// Mirage.
type streamEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"` // used by system + result

	// system/init fields (also present on system/api_retry).
	Model     string   `json:"model,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Tools     []string `json:"tools,omitempty"`

	// assistant / user events carry a `message` object whose `content` is an
	// array of typed blocks (text / tool_use / tool_result / thinking).
	Message json.RawMessage `json:"message,omitempty"`

	// system/api_retry fields.
	Attempt     int    `json:"attempt,omitempty"`
	Error       string `json:"error,omitempty"`
	ErrorStatus int    `json:"error_status,omitempty"`

	// result event fields. `result` is the final assistant text. `subtype` is
	// "success" | "error" | "error_max_turns". `errors` carries detailed
	// error messages when subtype is error_during_execution. `is_error` is
	// true for failures that still report subtype "success" (e.g. an invalid
	// API key).
	IsError      bool         `json:"is_error,omitempty"`
	Result       string       `json:"result,omitempty"`
	Errors       []string     `json:"errors,omitempty"`
	TotalCostUSD float64      `json:"total_cost_usd,omitempty"`
	Usage        *resultUsage `json:"usage,omitempty"`
	DurationMS   int64        `json:"duration_ms,omitempty"`

	// control_request / control_cancel_request carry a host-facing request
	// (tool permission prompts) identified by request_id.
	RequestID string          `json:"request_id,omitempty"`
	Request   json.RawMessage `json:"request,omitempty"`

	// stream_event carries a nested Anthropic API event in `event`. Its shape
	// depends on the nested `type` (message_start, content_block_start,
	// content_block_delta, content_block_stop, message_delta, message_stop).
	Event json.RawMessage `json:"event,omitempty"`
}

// controlRequest is the `request` object inside a `control_request` line.
// Only subtype "can_use_tool" is handled; any other subtype is answered with
// an error response so claude never blocks waiting on us.
type controlRequest struct {
	Subtype               string          `json:"subtype"`
	ToolName              string          `json:"tool_name,omitempty"`
	DisplayName           string          `json:"display_name,omitempty"`
	Input                 json.RawMessage `json:"input,omitempty"`
	Description           string          `json:"description,omitempty"`
	PermissionSuggestions json.RawMessage `json:"permission_suggestions,omitempty"`
	ToolUseID             string          `json:"tool_use_id,omitempty"`
}

// streamEventEnvelope is the nested `event` object inside a top-level
// `stream_event` line. Only the fields relevant to live text streaming are
// modeled; the rest are dropped.
type streamEventEnvelope struct {
	Type         string          `json:"type"` // message_start | content_block_start | content_block_delta | content_block_stop | message_delta | message_stop
	Index        int             `json:"index,omitempty"`
	Delta        json.RawMessage `json:"delta,omitempty"`
	ContentBlock json.RawMessage `json:"content_block,omitempty"`
}

// contentBlockStart is the `content_block` object inside a
// `content_block_start` stream event. Only the type is needed for text
// streaming (text blocks stream deltas; tool_use blocks stream input deltas
// which we ignore — the final assistant block carries the full input).
type contentBlockStart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// streamDelta is the `delta` object inside a `content_block_delta` stream
// event. text_delta carries a partial text fragment; input_json_delta carries
// partial tool input (ignored).
type streamDelta struct {
	Type string `json:"type"` // text_delta | input_json_delta | ...
	Text string `json:"text,omitempty"`
}

// resultUsage is the token usage summary on the terminal `result` event.
// Field names match Claude Code's stream-json output (Anthropic API naming).
type resultUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// messageEnvelope is the `message` object inside assistant/user events.
type messageEnvelope struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

// contentBlock is a single typed block within a message's content array. Only
// the fields relevant to Mirage are modeled; the rest are dropped.
type contentBlock struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// thinking block
	Thinking string `json:"thinking,omitempty"`

	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result block (appears in `user` events)
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}
