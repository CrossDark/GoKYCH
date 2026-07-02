package parsers

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

func parseWidgetAttrs(raw string) map[string]string {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// Find every `key="value"` pair AND every bare `key`
	// token. The two regexes together cover the full HTML5
	// attribute surface; a `key="value"` is preferred over
	// a bare `key` of the same name so an author can write
	// `checked="false"` to mean "explicitly off" without
	// getting a bare `checked` accidentally re-triggered
	// (we record the value "" for bare keys; the `=false`
	// form records `false` instead).
	for _, m := range reWDImageAttr.FindAllStringSubmatch(raw, -1) {
		key := strings.ToLower(strings.TrimSpace(m[1]))
		val := m[2]
		if key == "" {
			continue
		}
		out[key] = val
	}
	for _, m := range reWDBareAttr.FindAllStringSubmatch(raw, -1) {
		key := strings.ToLower(strings.TrimSpace(m[1]))
		if key == "" {
			continue
		}
		// Only set the bare-key form if `key="value"`
		// hasn't already been recorded for the same name.
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = ""
	}
	return out
}

// reWDBareAttr matches a bare attribute name (no `=value`)
// like `checked`, `selected`, `required`, `disabled`,
// `autofocus`, `readonly`, `multiple`. The lookahead ensures
// we don't double-match a name that's already part of a
// `key="value"` pair (the `key` half of `key="value"` is
// followed by `=`, not whitespace-or-end).
//
// Whitelisted to the well-known HTML5 boolean attribute set
// to avoid false-positives on substrings of a value (e.g. a
// comment mentioning "checked" inside a `placeholder="…"`
// should NOT turn into a bare attr). The list below is
// small and well-defined; adding more is safe but rarely
// needed.
var reWDBareAttr = regexp.MustCompile(`(?:^|\s)(checked|selected|required|disabled|autofocus|readonly|multiple|hidden|open|loop|muted|controls|default|autoplay|nowrap|noresize|defer|ismap|declare|compact|itemscope|sortable|truespeed|typemustmatch|async|defer|formnovalidate|novalidate|allowfullscreen|playsinline)\b`)

// buildSelfClosingWidget emits `<TAG k="v" … />` from a
// sanitised attribute map. Used by input / checkbox / radio.
// We always emit the tag in self-closing XML form so the
// HTML parser doesn't open an implicit element boundary.
//
// Attribute emission rules:
//   - `type` / `name` / `value` with non-empty value: `key="value"`.
//     An empty value here drops the attribute entirely
//     (HTML5 default kicks in for type, and `<input name>`
//     with no value is rare enough that omission is the
//     sensible default).
//   - Any other key with non-empty value: `key="value"`
//     (key sanitised via `sanitizeAnchorID`, value
//     HTML-escaped).
//   - Any non-type/name/value key with empty value: bare
//     `key`. This is the canonical form for HTML5 boolean
//     attributes (`checked`, `selected`, `required`, etc.)
//     and is also the legacy wikidot form for the same
//     surface.
func buildSelfClosingWidget(tag string, attrs map[string]string) string {
	keys := sortedAttrKeys(attrs)
	var sb strings.Builder
	sb.WriteString("<")
	sb.WriteString(tag)
	for _, k := range keys {
		v := attrs[k]
		switch {
		case k == "type" || k == "name" || k == "value":
			if v == "" {
				// Drop the attribute entirely; an empty
				// `type=""` or `name=""` is unusual and
				// the empty form is rare enough that
				// omission is safer than `<input type>`
				// (which most browsers parse as
				// `type=""` / "unspecified" with a
				// fallback to text).
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(k)
			sb.WriteString(`="`)
			sb.WriteString(html.EscapeString(v))
			sb.WriteString(`"`)
		case v != "":
			// `key="value"` form — sanitise name and HTML-
			// escape the value. Custom keys (`placeholder`,
			// `pattern`, `min`, `max`, `step`,
			// `data-*`, etc.) all flow through here.
			cleanKey := sanitizeAnchorID(k)
			if cleanKey == "" {
				continue
			}
			if k != cleanKey && !strings.HasPrefix(cleanKey, "data-") {
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(cleanKey)
			sb.WriteString(`="`)
			sb.WriteString(html.EscapeString(v))
			sb.WriteString(`"`)
		default:
			// Bare form (v == "" for any key other than
			// type/name/value). Canonical for HTML5 boolean
			// attributes (`checked`, `required`, etc.).
			cleanKey := sanitizeAnchorID(k)
			if cleanKey == "" {
				continue
			}
			if k != cleanKey && !strings.HasPrefix(cleanKey, "data-") {
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(cleanKey)
		}
	}
	sb.WriteString(" />")
	return sb.String()
}

// buildPairedWidget emits `<TAG k="v" … >inner</TAG>`.
// Used by button / textarea / select / option.
//
// Attribute emission rules mirror `buildSelfClosingWidget`:
//   - `type` / `name` / `value` with empty value → dropped.
//   - any other key with empty value → bare form.
//   - any key with non-empty value → `key="value"` (name
//     sanitised, value HTML-escaped).
func buildPairedWidget(tag string, attrs map[string]string, inner string) string {
	keys := sortedAttrKeys(attrs)
	var sb strings.Builder
	sb.WriteString("<")
	sb.WriteString(tag)
	for _, k := range keys {
		v := attrs[k]
		switch {
		case k == "type" || k == "name" || k == "value":
			if v == "" {
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(k)
			sb.WriteString(`="`)
			sb.WriteString(html.EscapeString(v))
			sb.WriteString(`"`)
		case v != "":
			cleanKey := sanitizeAnchorID(k)
			if cleanKey == "" {
				continue
			}
			if k != cleanKey && !strings.HasPrefix(cleanKey, "data-") {
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(cleanKey)
			sb.WriteString(`="`)
			sb.WriteString(html.EscapeString(v))
			sb.WriteString(`"`)
		default:
			cleanKey := sanitizeAnchorID(k)
			if cleanKey == "" {
				continue
			}
			if k != cleanKey && !strings.HasPrefix(cleanKey, "data-") {
				continue
			}
			sb.WriteString(" ")
			sb.WriteString(cleanKey)
		}
	}
	sb.WriteString(">")
	sb.WriteString(inner)
	sb.WriteString("</")
	sb.WriteString(tag)
	sb.WriteString(">")
	return sb.String()
}

// sortedAttrKeys returns the attribute keys in deterministic
// order so the rendered HTML is stable across runs (helps
// snapshot tests and diff-driven reviews).
func sortedAttrKeys(attrs map[string]string) []string {
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		// Put `type`, `name`, `value` first so the rendered
		// tag looks like HTML's typical attribute order.
		prio := func(s string) int {
			switch s {
			case "type":
				return 0
			case "name":
				return 1
			case "value":
				return 2
			case "checked", "selected", "disabled", "required", "readonly", "autofocus":
				return 3
			}
			return 4
		}
		pi, pj := prio(keys[i]), prio(keys[j])
		if pi != pj {
			return pi < pj
		}
		return keys[i] < keys[j]
	})
	return keys
}
