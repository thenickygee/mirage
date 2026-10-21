package ui

import (
	"github.com/thenickygee/mirage/internal/harness"
)

// originTag returns the plain-text harness tag for a definition, e.g. "CC",
// "OC·project" or "CC·vercel" for a plugin item.
func originTag(o harness.Origin) string {
	var tag string
	switch o.Harness {
	case harness.Claude:
		tag = "CC"
	case harness.OpenCode:
		tag = "OC"
	default:
		return ""
	}
	switch o.Scope {
	case harness.ScopeProject:
		tag += "·project"
	case harness.ScopePlugin:
		tag += "·" + o.Plugin
	}
	return tag
}

// originBadge is originTag colored by harness, for rows without a highlight
// background.
func originBadge(o harness.Origin) string {
	tag := originTag(o)
	switch o.Harness {
	case harness.Claude:
		return StyleBadgeSourceCC.Render(tag)
	case harness.OpenCode:
		return StyleBadgeSourceOC.Render(tag)
	}
	return ""
}

// listOriginSuffix is the trailing tag for a list row. Selected rows use plain
// text so the highlight background has no unstyled gaps.
func listOriginSuffix(o harness.Origin, selected bool) string {
	tag := originTag(o)
	if tag == "" {
		return ""
	}
	if selected {
		return "  " + tag
	}
	return "  " + originBadge(o)
}

// originDetail is the badge shown in a detail header: harness tag plus the
// source path scope, with a leading gap, or empty when the origin is unknown.
func originDetail(o harness.Origin) string {
	b := originBadge(o)
	if b == "" {
		return ""
	}
	return "  " + b
}
