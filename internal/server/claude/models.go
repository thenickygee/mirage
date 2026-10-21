package claude

// contextWindows maps Claude model IDs to their context-window size in tokens.
// Claude Code's system/init event reports the model ID but not its context
// limit, so we ship a small static map (mirrors the opencode client's
// modelLimits). Unknown IDs fall back to defaultContextWindow.
//
// Keep this in sync as Anthropic ships new models; update via Phase 3's
// optional fetch-from-Anthropic-API path if/when added.
var contextWindows = map[string]int64{
	"claude-sonnet-4-5":          200000,
	"claude-sonnet-4-20250514":   200000,
	"claude-opus-4-1":            200000,
	"claude-opus-4-20250805":     200000,
	"claude-haiku-4-5":           200000,
	"claude-3-7-sonnet-20250219": 200000,
	"claude-3-5-sonnet-20241022": 200000,
	"claude-3-5-haiku-20241022":  200000,
}

// defaultContextWindow is the fallback when a model ID is unknown.
const defaultContextWindow int64 = 200000

// contextWindowFor returns the context window for a model ID, or the default
// if unknown.
func contextWindowFor(modelID string) int64 {
	if cw, ok := contextWindows[modelID]; ok && cw > 0 {
		return cw
	}
	return defaultContextWindow
}
