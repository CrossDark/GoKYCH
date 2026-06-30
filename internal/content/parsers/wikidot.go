package parsers

import (
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Wikidot regexes ────────────────────────────────────────────────────

// advListTok is the token shape used by
// renderWikidotAdvancedLists. Defined at package
// scope so the sortToks helper (which uses a
// structurally-equivalent anonymous-struct
// parameter for decoupling) can compile cleanly
// without exporting a private struct.
type advListTok struct {
	pos   int
	end   int
	kind  string // "ul" | "ol" | "li" | "/li" | "/ul" | "/ol"
	raw   string
	attrs string
}

//
// Coverage matches what authors actually write on the site, not the full
// Wikidot spec. Block patterns are stored as placeholders so the inner
// content doesn't get re-processed by inline passes.
//
// `[[include]]`, `[[module …]]`, `[[toc]]`, `%%var%%`, footnotes, and
// nested lists were added on top of the basic wikidot syntax (the
// static subset, which the previous parser already covered). All of
// them run off an optional *RenderContext — when no context is passed
// in, the dynamic constructs degrade to their raw source (matching the
// pre-existing behaviour so SSR for /api/articles without a context
// keeps working).

var (
	reWDCode = regexp.MustCompile(`(?is)\[\[code(?:\s+type\s*=\s*['"]([^'"]+)['"])?\]\](.*?)\[\[/code\]\]`)
	// [[[wikidot link]]] is matched in Phase 3 (links) alongside
	// [url text] and [mailto:…] external link forms.

	// reBlockMarkerInLine splits a paragraph-wrap line on
	// `%%BLOCK_N%%` / `%%WRAP_BLOCK_N%%` placeholders. Used by
	// wrapWikidotParagraphs to keep block-level placeholders
	// out of `<p>…</p>` wrappers (where they'd be invalid HTML
	// after the placeholder is restored in Phase 10).
	reBlockMarkerInLine = regexp.MustCompile(`(.*?)(%%(?:BLOCK|WRAP_BLOCK)_\d+%%)(.*)`)

	reWDDiv      = regexp.MustCompile(`(?is)\[\[div\s+class="([^"]*)"\]\](.*?)\[\[/div\]\]`)
	reWDDivStyle = regexp.MustCompile(`(?is)\[\[div\s+style="([^"]*)"\]\](.*?)\[\[/div\]\]`)
	reWDDivFloat = regexp.MustCompile(`(?is)\[\[float\s*=\s*(left|right)\s*\]\](.*?)\[\[/float\]\]`)
	reWDTable    = regexp.MustCompile(`(?is)\[\[table\]\](.*?)\[\[/table\]\]`)
	// Row-based table syntax: lines starting with `||` and ending with `||`.
	// Each `||` is a cell separator; `||~ header ~||` denotes a header cell.
	// Merged-cell syntax `|| ||` (empty) or `||||` collapses to one cell with
	// colspan.
	reWDTableRowLine = regexp.MustCompile(`(?m)^\s*\|\|[^|\n]*?(?:\|\|[^|\n]*?)*\|\|\s*$`)

	reWDSpanClass   = regexp.MustCompile(`(?is)\[\[span\s+class="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDSpanStyle   = regexp.MustCompile(`(?is)\[\[span\s+style="([^"]*)"\]\](.*?)\[\[/span\]\]`)
	reWDCollapsible = regexp.MustCompile(`(?is)\[\[collapsible((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\](.*?)\[\[/collapsible\]\]`)
	// reWDCollapsibleAttr matches a single `key="value"` pair
	// inside a `[[collapsible ...]]` block — used to pull
	// the `show` / `hide` / `folded` / `hideLocation` keys out
	// of the attribute tail captured by reWDCollapsible.
	reWDCollapsibleAttr = regexp.MustCompile(`([a-zA-Z][\w-]*)\s*=\s*"([^"]*)"`)
	reWDSize            = regexp.MustCompile(`(?is)\[\[size\s+([^\]]+)\]\](.*?)\[\[/size\]\]`)
	reWDColor           = regexp.MustCompile(`(?is)\[\[color\s+([^\]]+)\]\](.*?)\[\[/color\]\]`)
	// [[bgcolor name]]…[[/bgcolor]] — Wikidot's block-form
	// background-colour span. Companion to [[color]]; uses
	// the same `colorNames` lookup plus raw CSS passthrough
	// so `[[bgcolor yellow]]`, `[[bgcolor #f0f0f0]]`, and
	// `[[bgcolor rgba(0,0,0,0.1)]]` all work (subject to
	// the usual sanitizeCSSValue rejection of dangerous
	// tokens).
	reWDBgcolor = regexp.MustCompile(`(?is)\[\[bgcolor\s+([^\]]+)\]\](.*?)\[\[/bgcolor\]\]`)
	// [[font name]]…[[/font]] — change the font-family of
	// the wrapped text. The name can be a CSS font stack
	// (commas allowed; quote-safe via sanitizeCSSValue).
	reWDFont = regexp.MustCompile(`(?is)\[\[font\s+([^\]]+)\]\](.*?)\[\[/font\]\]`)
	// [[indent]]…[[/indent]] — Wikidot's indent block.
	// Renders to `<div class="wikidot-indent">` so the
	// front-end can decide how much to indent (CSS-driven,
	// theme-aware). Nesting is implicit: each level adds
	// a wrapping `<div>` so the CSS `padding-left` adds
	// up the same way Wikidot's `[[indent]]` does.
	reWDIndent = regexp.MustCompile(`(?is)\[\[indent\]\](.*?)\[\[/indent\]\]`)
	// [[iframe URL]] / [[iframe URL width height]] —
	// Wikidot's direct iframe embed. URL is run through
	// sanitizeURLForAttr so a `javascript:` or
	// `data:text/html` payload can't smuggle script in.
	// Width / height are optional (defaults 100% × 400).
	reWDIframe = regexp.MustCompile(`(?i)\[\[iframe\s+([^\s\]]+)(?:\s+(\d+)\s+(\d+))?\]\]`)
	// [[video URL]] / [[video URL width height]] — HTML5
	// `<video>` embed. Same URL sanitisation as iframe;
	// width / height optional (defaults 100% × auto).
	reWDVideo = regexp.MustCompile(`(?i)\[\[video\s+([^\s\]]+)(?:\s+(\d+)\s+(\d+))?\]\]`)
	// [[audio URL]] — HTML5 `<audio>` embed. URL
	// sanitised like the other media forms. Width /
	// height are ignored (audio has no intrinsic size);
	// we render `<audio controls src="…">` and let the
	// browser's native control chrome decide the layout.
	reWDAudio = regexp.MustCompile(`(?i)\[\[audio\s+([^\s\]]+)\]\]`)
	// [[date]] / [[date format]] — current date.
	// Format is a Go time format string; the renderer
	// substitutes it directly. When the format is omitted
	// we default to the site-friendly `2006-01-02`
	// (Wikidot's default behaviour; the author can
	// override per locale).
	reWDDate = regexp.MustCompile(`(?i)\[\[date(?:\s+([^\]]+))?\]\]`)
	// [[tabview]]…[[/tabview]] + [[tab Title]]…[[/tab]].
	// The opener / closer pair is matched by a depth-
	// counting scanner (not a regex) so a nested
	// `[[tabview]]` inside a tab body doesn't break the
	// outer match — same posture as the indent block.
	// The per-tab regex below is what splits the body
	// once the outer span is identified; it captures
	// (title, content) and uses the same `.*?` non-
	// greedy convention since tabs don't nest.
	reWDTabOpen  = regexp.MustCompile(`(?is)\[\[tabview\]\]`)
	reWDTabClose = regexp.MustCompile(`(?is)\[\[/tabview\]\]`)
	reWDTabItem  = regexp.MustCompile(`(?is)\[\[tab\s+([^\]\n]+?)\]\](.*?)\[\[/tab\]\]`)
	reWDMath     = regexp.MustCompile(`(?is)\[\[math\]\](.*?)\[\[/math\]\]`)
	reWDHTMLRaw  = regexp.MustCompile(`(?is)\[\[html\]\](.*?)\[\[/html\]\]`)
	// YouTube ID — Wikidot's real rule is broader than `[A-Za-z0-9_-]{6,20}`
	// (it accepts 11-char base64-ish IDs plus our authors occasionally paste
	// long, oddly-formatted test strings like 中文). Loosen to a generic
	// "any non-bracket non-space run" and rely on the client-side JS
	// pass to ignore ids the iframe can't load.
	reWDYoutube   = regexp.MustCompile(`(?i)\[\[youtube\s+([^\s\]]+?)\s*\]\]`)
	reWDAnchorDef = regexp.MustCompile(`(?i)\[\[a\s+name\s*=\s*"?([^"\]]+?)"?\s*\]\]`)
	// Paired form: `[[a name="x"]]content[[/a]]` — wraps the inner block
	// in a span with id="x" so the [#x text] jump-link below can land
	// on it. Same anchor id rules as reWDAnchorDef (HTML-escape the id;
	// we don't constrain to ASCII because the test doc uses Chinese ids).
	reWDAnchorPair = regexp.MustCompile(`(?is)\[\[a\s+name\s*=\s*"?([^"\]]+?)"?\s*\]\](.*?)\[\[/a\]\]`)
	// [[toc]] — replaced in Phase 12 with a nested <ul> of the h2/h3
	// headings collected during Phase 4. We stash the match with a
	// placeholder so later passes don't touch it.
	reWDTOC   = regexp.MustCompile(`\[\[toc\]\]`)
	reWDFLTOC = regexp.MustCompile(`\[\[f<toc\]\]`)
	reWDFRTOC = regexp.MustCompile(`\[\[f>toc\]\]`)
	// [[include SLUG | key=val | key=val]] — recursive page embed.
	// SLUG can be "category:page-name" (Wikidot colon-namespace) or
	// "page-name". The kv tail is a space- or pipe-separated
	// attribute list; the include'd page's %%var%% substitutions
	// see the keys, which is how Wikidot templates work.
	reWDInclude = regexp.MustCompile(`(?is)\[\[include\s+([^\s\]]+?)((?:\s*\|\s*[^\]]*?)?)\]\]`)
	// [[module Name key="val" key="val"]] … [[/module]] — block module.
	// We do paired matching in convert() so the inner template
	// (which can contain its own %%, *, [[ ]]) is captured as a
	// whole before any inline pass.
	reWDModuleOpen  = regexp.MustCompile(`(?is)\[\[module\s+([A-Za-z]+)((?:\s+[^\]]*?)?)\]\]`)
	reWDModuleClose = regexp.MustCompile(`\[\[/module\]\]`)

	// ── Stage 4 (P1 round 3) additions ───────────────────────────────
	//
	// These close the spec gaps identified against
	// https://rule-wiki.wikidot.com/wiki-syntax — see gap analysis
	// in the convert() comment block. Each regex is paired with a
	// test in wikidot_test.go pinning its semantics so future
	// refactors don't silently drop the new syntax.

	// HTML comments `[!-- ... --]`. Wikidot comments are stripped
	// entirely from output (the content between the delimiters
	// never renders). The match is non-greedy across newlines so
	// a multi-line comment (`[!--\nfoo\n--]`) is consumed in one
	// shot. Run BEFORE any other pass so the inner content (which
	// may contain `**`, `//`, `[[ ]]` that would otherwise feed
	// the inline stack) is dropped wholesale.
	reWDComment = regexp.MustCompile(`(?s)\[!--.*?--\]`)

	// `[# empty link]` — Wikidot's no-op placeholder link. The
	// leading space after `#` discriminates a real anchor
	// jump-link (`[#name text]`) from a placeholder (`[# any
	// display text]`) that renders as a normal-looking link whose
	// click does nothing (`href="javascript:;"`).
	reWDEmptyLink = regexp.MustCompile(`\[#\s+([^\]]+)\]`)

	// `[[# name]]` — alternative form for the `[[a name="..."]]`
	// anchor def. Wikidot accepts both syntaxes; the `[[# ]]`
	// form is more compact (no `a name=` keyword) and mirrors
	// the jump-link syntax `[#name]` symmetrically. Matched
	// before block-stash phases that might otherwise trip on
	// the `[[`.
	reWDHashAnchorDef = regexp.MustCompile(`(?is)\[\[#\s+([^\]\n]+?)\s*\]\]`)

	// Triple-bracket starred link `[[[*http://...|Text]]]`. Same
	// as `[[[http://...|Text]]]` but the `*` after `[[[` opens
	// the link in a new tab (`target="_blank"` + `rel="nofollow
	// noopener"`). Wikidot's convention for "open in new window"
	// on triple-bracket links. Match `]]]` (3 closes) to
	// match the open triple — the ordinary wiki-link regex
	// uses the same 3-close form.
	reWDStarredTripleLink = regexp.MustCompile(`(?is)\[\[\[\*(https?://[^\s\]]+?)(?:\s*\|\s*([^\]]+?))?\]\]\]`)
	// Note: the trailing `\]\]\]` matches 3 closes — see the
	// reWDWikiLink regex (`\[\[\[([^\]]+?)(?:\s*\|\s*([^\]]+?))?\]\]\]`)
	// which also uses 3 closes for the same reason.

	// Single-bracket starred link `[*http://... text]`. Mirror of
	// the triple-bracket `[[[*...]]]` form — same new-window
	// semantics. Matched before the plain `[url text]` so the `*`
	// is consumed as part of the link markup.
	reWDStarredLink = regexp.MustCompile(`\[\*(https?://[^\s\]]+)(?:\s+([^\]]+))?\]`)

	// Bare starred URL `*http://...` (auto-link form with the
	// "open in new window" prefix). Wikidot's convention: when an
	// author writes `*http://example.com` in flowing text without
	// brackets, the link opens in a new tab.
	reWDBareStarredURL = regexp.MustCompile(`(?i)(\*https?://[^\s<>\[\]]+)`)

	// Relative-path single-bracket link `[/path text]`. Same
	// shape as `[http://url text]` but without a protocol —
	// treated as a wikidot-internal navigation URL on the
	// current site. Useful for short-form links like
	// `[/blog:post/edit/true 编辑这页]` that don't need a full
	// https URL.
	//
	// The discriminator between a real relative-link URL and a
	// wikidot closing tag (`[/li]`, `[/ul]`, `[/div]`, `[/email]`,
	// `[/note]`, …) is the requirement that the path contain at
	// least one path-separator character: `/`, `:`, `.`, or `-`.
	// A single-word alphabetic token like `li` / `ul` / `div` is
	// always a wikidot closing tag, never an internal URL. We
	// require the path to have at least one of the separator
	// characters so `[/li]` and friends don't false-positive.
	reWDRelativeLink = regexp.MustCompile(`\[(/[^\s\]]*[/.:\-][^\s\]]*)(?:\s+([^\]]+))?\]`)

	// ── Stage 5 (P2 round 4) — gallery + form widgets ─────────────
	//
	// Closes the next round of gaps vs the wikidot syntax spec.
	// Each regex here pairs with a test in wikidot_test.go.

	// `[[gallery]]` … `[[/gallery]]` block — Wikidot's image-gallery
	// form. The body is line-delimited; each line is either `URL`
	// (just the URL) or `URL | caption` (URL + optional caption).
	// The form takes no attributes. Nested `[[image …]]` constructs
	// inside a gallery block are NOT accepted — Wikidot's gallery
	// form is intentionally the simple `URL [| caption]`-per-line
	// layout, and supporting the heavier `[[image …]]` inside the
	// gallery would mean nesting the Phase-1 stash passes (which the
	// gallery block is already running past).
	reWDGallery = regexp.MustCompile(`(?is)\[\[gallery\]\](.*?)\[\[/gallery\]\]`)

	// Form widgets — each construct accepts an arbitrary attribute
	// tail `key="value" key="value" …` shared with `[[image]]`. The
	// shared `reWDImageAttr` regex (the same one image uses) is
	// reused at parse time so authors learn one attribute syntax.
	//
	// `[[form attrs]]` opener — paired with `[[/form]]` close.
	// `[[input attrs]]` single-tag — auto-closes (no inner content).
	// `[[textarea attrs]]` opener — paired with `[[/textarea]]`.
	// `[[button attrs]]` single-tag — `label="…"` attribute is
	//                               rendered as the button's inner
	//                               text (HTML <button> uses inner
	//                               text, not a label attr).
	// `[[checkbox attrs]]` — `checked` attribute (no value) renders
	//                       as the bare HTML attribute when present.
	// `[[radio attrs]]` — single-tag `<input type="radio">`.
	// `[[select attrs]]` opener — paired with `[[/select]]` close.
	// `[[option attrs]]` opener — paired with `[[/option]]` close;
	//                          the inner text is the option's label.
	reWDFormOpen       = regexp.MustCompile(`(?is)\[\[form((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDFormClose      = regexp.MustCompile(`\[\[/form\]\]`)
	reWDInput          = regexp.MustCompile(`(?is)\[\[input((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDTextareaOpen   = regexp.MustCompile(`(?is)\[\[textarea((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDTextareaClose  = regexp.MustCompile(`\[\[/textarea\]\]`)
	// NOTE: `reWDButton` (the legacy wikidot button used at line
	// ~487 for `[[button Label|target]]`) keeps that name. Our
	// form-widget button — `[[button attrs]]` with arbitrary
	// attributes but no `|` separator — is named `reWDFormButton`
	// so the two syntaxes stay distinct: a `[[button label|…]]`
	// passes the legacy regex, a `[[button type="…" …]]` passes
	// this one. The order in which the two are tried determines
	// which one wins.
	reWDFormButton     = regexp.MustCompile(`(?is)\[\[button((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDCheckbox       = regexp.MustCompile(`(?is)\[\[checkbox((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDRadio          = regexp.MustCompile(`(?is)\[\[radio((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDSelectOpen     = regexp.MustCompile(`(?is)\[\[select((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDSelectClose    = regexp.MustCompile(`\[\[/select\]\]`)
	reWDOptionOpen     = regexp.MustCompile(`(?is)\[\[option((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDOptionClose    = regexp.MustCompile(`\[\[/option\]\]`)

	// `reWDSizeValue` validates the non-keyword numeric form of
	// a `[[size …]]` value. Matches `N`, `Npx`, `Nem`, `N%` with
	// optional fractional part (e.g. `0.8em`, `18.75px`, `50%`).
	// Anything else — including bogus CSS keywords like
	// `[[size giant]]`, `[[size huger]]`, `[[size red]]` — fails
	// this whitelist, so the size span degrades to plain text
	// rather than producing `style="font-size:giant"` (which
	// browsers silently ignore but would-be readers can't see
	// as a typo).
	reWDSizeValue = regexp.MustCompile(`^\s*\d+(\.\d+)?(px|em|%)?\s*$`)

	// ── Image alignment prefixes ──────────────────────────────────
	//
	// Wikidot's image-position system lets the author write
	// `[[=image URL attrs]]` (center) / `[[<image ...]]` (left) /
	// `[[>image ...]]` (right) / `[[f<image ...]]` (left float,
	// text wraps) / `[[f>image ...]]` (right float, text wraps).
	// All five forms take the same attribute tail as the plain
	// `[[image URL attrs]]` — we route them through the same
	// `buildImageTag` / `parseImageAttrs` helpers used by the
	// non-prefixed `[[image]]` form. Each prefix is captured as
	// a wrapping `<div class="wikidot-image-wrap wikidot-image-<align>">`
	// so the front-end can style alignment vs float independently.
	reWDImgCenter = regexp.MustCompile(`(?is)\[\[=\s*image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDImgLeft   = regexp.MustCompile(`(?is)\[\[<\s*image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDImgRight  = regexp.MustCompile(`(?is)\[\[>\s*image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDImgFloatL = regexp.MustCompile(`(?is)\[\[f<\s*image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDImgFloatR = regexp.MustCompile(`(?is)\[\[f>\s*image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)

	reWDCenter    = regexp.MustCompile(`(?s)\[\[=\]\](.*?)\[\[/=\]\]`)
	reWDLeftBlock = regexp.MustCompile(`(?s)\[\[<\]\](.*?)\[\[/<\]\]`)
	reWDRight     = regexp.MustCompile(`(?s)\[\[>\]\](.*?)\[\[/>\]\]`)
	reWDJustify   = regexp.MustCompile(`(?s)\[\[==\]\](.*?)\[\[/==\]\]`)

	// Single-line alignment shortcuts. The Wikidot spec lets
	// authors start a line with `= text` (center), `< text`
	// (left) to apply a one-line alignment block — the
	// block form `[[=]]...[[/=]]` is for multi-line
	// content. The `<` form has to be on its own line and
	// followed by whitespace to distinguish it from the
	// less-than operator (which doesn't really appear in
	// wikidot text, but we keep the leading-anchor rule for
	// safety).
	reWDCenterLine = regexp.MustCompile(`(?m)^=\s+(.+)$`)
	reWDLeftLine   = regexp.MustCompile(`(?m)^<\s+(.+)$`)

	// `+*` heading prefix — same leading-plus count as the
	// normal `+` / `++` / etc. heading, but the trailing
	// `*` marks the heading as "skip in TOC". We still
	// emit a stable anchor id (so `[#name text]` and
	// `[[[page#anchor]]]` keep working) — only the TOC
	// builder filters these out. Multi-line mode so each
	// `+*` heading is matched on its own line.
	reWDH1Star = regexp.MustCompile(`(?m)^\+\*\s+(.+)$`)
	reWDH2Star = regexp.MustCompile(`(?m)^\+\+\*\s+(.+)$`)
	reWDH3Star = regexp.MustCompile(`(?m)^\+\+\+\*\s+(.+)$`)
	reWDH4Star = regexp.MustCompile(`(?m)^\+\+\+\+\*\s+(.+)$`)
	reWDH5Star = regexp.MustCompile(`(?m)^\+\+\+\+\+\*\s+(.+)$`)
	reWDH6Star = regexp.MustCompile(`(?m)^\+\+\+\+\+\+\*\s+(.+)$`)

	// Inline formatting (Phase 2). Bold/italic/underline/etc. kept verbatim
	// from the original parser; new additions below.
	reWDSuperscript = regexp.MustCompile(`\^\^(.+?)\^\^`)
	reWDSubscript   = regexp.MustCompile(`,,(.+?),,`)
	reWDAutoURL     = regexp.MustCompile(`(?i)\b(https?://[^\s<>\[\]]+)`)
	reWDLineBreak   = regexp.MustCompile(`(?i)\[\[br\]\]`)
	// `[[user name]]` and `[[*user name]]` (the `*` form is
	// "logged-in" / "staff" highlighting; we emit the same
	// markup with a slightly different CSS hook so the
	// article view can theme it later). The captured
	// name is the wikidot-style display text — until the
	// real UserLookup interface is added we render the
	// raw name as a span. The two pieces that the
	// parser can answer without a user database are
	// "is this a well-formed user mention" (the regex
	// matches) and "what username did the author
	// write" (the capture). Everything else (avatar,
	// display name, profile link) belongs to a future
	// round that wires a UserLookup into RenderContext.
	reWDUser = regexp.MustCompile(`(?i)\[\[\*?user\s+([^\]\n]+?)\]\]`)
	// Jump-link `[#name]` or `[#name text]` (Wikidot uses SINGLE
	// brackets here, unlike the `[[a name=…]]` anchor def above).
	// When text is present, emit a clickable anchor that scrolls to
	// the matching id="name" span emitted by reWDAnchorDef/Pair;
	// without text, fall back to a self-anchor span (rare — used to
	// drop an anchor into a position the author can reference from
	// elsewhere).
	//
	// Note: no `\s+` between `#` and the name — Wikidot uses `[#x]`
	// without a space, but `[#x alias text]` has a space before the
	// alias. Treat the boundary as "non-`]` non-whitespace" so the
	// name capture is greedy.
	reWDAnchor = regexp.MustCompile(`\[#([^\]\s]+)(?:\s+([^\]]+))?\]`)
	// reWDLiteral — `@@...@@` is the Wikidot literal-escape
	// construct: the inner text is rendered VERBATIM, with
	// no wikidot markup expansion. We stash the inner text
	// as a block (HTML-escaped) in Phase 0 so that every
	// downstream phase — Phase 1 (block storage), Phase 1.5
	// (TOC), Phase 2 (inline formatting), Phase 9 (lists) —
	// sees the placeholder, not the inner markup. The block
	// is restored in Phase 10 with the inner text already
	// entity-escaped, so even content like `**` or `[[code]]`
	// inside `@@...@@` survives the round-trip unchanged.
	//
	// Note: this is a behavioural change from the previous
	// parser, which treated `@@...@@` as monospace (a `<code>`
	// wrapper). Wikidot's actual spec uses `@@...@@` for
	// literal escape; monospace wikidot text is a separate
	// feature (`{{...}}`, also still supported).
	reWDLiteral = regexp.MustCompile(`@@([^@]+?)@@`)
	reWDBold    = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// Italic `//x//` — the opening `//` must NOT be preceded by `:`,
	// so URLs like `https://example.com` (which contain `://`) don't
	// false-positive when they appear inside HTML tags the auto-linker
	// produced. The first capture holds the safe prefix (`^` or a
	// non-`:` char) which the replace helper re-emits unchanged.
	reWDItalic        = regexp.MustCompile(`(?m)(^|[^:])//(.+?)//`)
	reWDUnderline     = regexp.MustCompile(`__(.+?)__`)
	reWDStrikethrough = regexp.MustCompile(`--(.+?)--`)
	reWDInlineCode    = regexp.MustCompile(`\{\{(.+?)\}\}`)
	// Inline colour in the Wikidot "##color|text##" form (no brackets).
	// The colour can be either a name from `colorNames` (e.g.
	// "blue", "red") or a CSS hex value (e.g. "#44FF88", "#fff").
	// Must run before the bold/italic passes — `**` and `##` are
	// syntactically unrelated, but processing colour early keeps the
	// pipeline simple.
	// Inline colour in the Wikidot "##color|text##" form (no brackets).
	// The colour is one of three shapes (see applyWikidotInlineColor):
	//   - a CSS-named token starting with a letter (e.g. "blue",
	//     "lightblue", "dark-red");
	//   - hex with leading `#` (3-8 digits, e.g. "#44FF88", "#fa3");
	//   - hex WITHOUT leading `#` (3-8 digits, e.g. "44FF88") — the
	//     bare-hex form used by the rule-wiki wikidot-syntax spec.
	//
	// The named alternative is anchored to `[A-Za-z]` so a hex like
	// "44FF88" doesn't accidentally fall through the name path
	// (where it wouldn't match anything in colorNames anyway, but
	// the bare-hex probe below needs the unambiguous split).
	//
	// Must run before the bold/italic passes — `**` and `##` are
	// syntactically unrelated, but processing colour early keeps the
	// pipeline simple.
	reWDInlineColor = regexp.MustCompile(`##([A-Za-z][\w-]*|#[0-9A-Fa-f]{3,8}|[0-9A-Fa-f]{3,8})\|([^#]+?)##`)

	// Phase 3 links — external [url text] and mailto [mailto:addr text].
	// Wikidot's internal link form `[[[page|alias]]]` is below.
	reWDExternalLink = regexp.MustCompile(`\[(https?://[^\s\]]+)(?:\s+([^\]]+))?\]`)
	reWDMailto       = regexp.MustCompile(`\[mailto:([^\s\]]+)(?:\s+([^\]]+))?\]`)
	reWDWikiLink     = regexp.MustCompile(`\[\[\[([^\]]+?)(?:\s*\|\s*([^\]]+?))?\]\]\]`)
	// [[image URL key="value" ...]] — generic attribute syntax.
	// Supported keys (Wikidot spec):
	//   - link="..." wraps the <img> in an <a href="...">
	//   - width="Npx" sets the rendered width
	//   - height="Npx" sets the rendered height
	//   - class="..." applies a class
	//   - style="..." applies inline CSS
	// Unknown attributes are silently dropped (no warning,
	// matching the spec's "anything else is ignored" behaviour).
	// The regex captures the URL in group 1 and the
	// attribute tail in group 2; an unquoted or
	// alternate-form attribute would not match — Wikidot
	// always uses `key="value"` with double quotes.
	reWDImage = regexp.MustCompile(`(?i)\[\[image\s+([^\s\]]+)((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	// reWDImageAttr matches a single `key="value"` pair, used
	// by parseImageAttrs to break the attribute tail captured
	// by reWDImage into a map.
	reWDImageAttr = regexp.MustCompile(`([a-zA-Z][\w-]*)\s*=\s*"([^"]*)"`)

	reWDH6_ = regexp.MustCompile(`(?m)^\+\+\+\+\+\+\s+(.+)$`)
	reWDH5_ = regexp.MustCompile(`(?m)^\+\+\+\+\+\s+(.+)$`)
	reWDH4_ = regexp.MustCompile(`(?m)^\+\+\+\+\s+(.+)$`)
	reWDH3_ = regexp.MustCompile(`(?m)^\+\+\+\s+(.+)$`)
	reWDH2_ = regexp.MustCompile(`(?m)^\+\+\s+(.+)$`)
	reWDH1_ = regexp.MustCompile(`(?m)^\+\s+(.+)$`)
	// reWDHeading — single regex matching any wikidot heading
	// line, including the `+*` SkipTOC variant. The captures
	// are (1) plus-sign count, (2) optional `*` marker, (3)
	// heading text. The level is `len(m[1])` so H1 = `+`,
	// H6 = `++++++`. We keep the per-level regexes above for
	// now (they're referenced by headingRegexFor below) but
	// Phase 4 only uses reWDHeading so the order in which
	// headings appear in `p.headings` matches source order
	// (the previous level-by-level pipeline processed H6
	// first then H5 … H1, reversing TOC order whenever a
	// single article mixed levels).
	reWDHeading = regexp.MustCompile(`(?m)^(\+{1,6})(\*?)\s+(.+)$`)

	reWDBlockquote    = regexp.MustCompile(`(?m)^(?:&gt;|>)\s?(.*)$`)
	reWDUnorderedItem = regexp.MustCompile(`(?m)^(\s*)\*\s+(.+)$`)
	reWDOrderedItem   = regexp.MustCompile(`(?m)^(\s*)#\s+(.+)$`)
	// Definition-list line: `: term : definition`.
	// The leading `:` and the separator `: ` are required.
	// We capture (term, definition) in two groups. A
	// continuation line is one whose leading `:` is
	// followed by non-empty text (handled by
	// renderWikidotDefList via a second regex).
	reWDDefItem = regexp.MustCompile(`(?m)^: ([^:\n][^\n]*?) : (.+?)$`)
	reWDDefCont = regexp.MustCompile(`(?m)^: (.+?)$`)

	// Advanced list block syntax: `[[ul]]`, `[[ol]]`,
	// `[[li ...]]` with arbitrary `class="..."`,
	// `style="..."`, `data-...="..."` attributes. The
	// `[[li]]` body is a sub-wikidot source (inline
	// formatting + nested block lists).
	reWDUL         = regexp.MustCompile(`(?is)\[\[ul((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDOL         = regexp.MustCompile(`(?is)\[\[ol((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDLIOpen     = regexp.MustCompile(`(?is)\[\[li((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
	reWDLIClose    = regexp.MustCompile(`(?is)\[\[/li\]\]`)
	reWDULClose    = regexp.MustCompile(`(?is)\[\[/ul\]\]`)
	reWDOLClose    = regexp.MustCompile(`(?is)\[\[/ol\]\]`)
	reWDHR         = regexp.MustCompile(`(?m)^-{3,}$`)
	reWDAdmonition = regexp.MustCompile(`(?sm)^!!!\s+(note|warning|danger|info|tip)\s*\n(.*?)(\n!!!|\n\[\[|\z)`)
	// [[divider]] — Wikidot's themed horizontal rule.
	// `[[divider]]` renders to the same `<hr>` as the
	// `----` line form but with a CSS class so the
	// front-end can give it a different look (a faint
	// double rule, a coloured band, etc.) without
	// touching the plain `<hr>` used elsewhere.
	reWDDivider = regexp.MustCompile(`(?i)\[\[divider\]\]`)
	// [[note]]…[[/note]] — inline note box. The
	// difference vs the `!!! note` admonition is
	// scope: `[[note]]` is a single-paragraph callout
	// that can sit mid-paragraph, while the
	// admonition is a multi-line block. We render
	// both to the same DOM shape (`.wikidot-note`)
	// and let CSS decide if they're visually
	// different. Body is converted recursively so
	// inline formatting (`**bold**`, `//italic//`,
	// links) survives.
	reWDNote = regexp.MustCompile(`(?is)\[\[note\]\](.*?)\[\[/note\]\]`)
	// [[button label]] / [[button label|target]] —
	// render a Wikidot-style button link. The label
	// is mandatory; the target defaults to "#" when
	// omitted. Pipe form (`label|target`) is supported
	// so authors can put spaces in the label without
	// ambiguity; the bare form (`label`) is the
	// short-hand for a placeholder button. When the
	// target looks external (http(s)://) the renderer
	// adds `rel="nofollow noopener" target="_blank"`
	// — same posture as the `[url text]` external
	// link. Internal targets (paths starting with `/`
	// or wiki-page names) render as plain anchor
	// links.
	reWDButton = regexp.MustCompile(`(?i)\[\[button\s+([^\]\n|]+)(?:\|([^\]\n]+))?\]\]`)
	// [[email]]address[[/email]] or
	// [[email address]] — Wikidot renders emails as
	// obfuscated `<a>` tags (the address is
	// broken into character spans or split on `@` so
	// naive scrapers can't harvest it). For our
	// purposes the simplest form is good enough:
	// emit an `<a class="wikidot-email">` with a
	// `data-user` + `data-domain` split so a small
	// client-side script (or just CSS hover) can
	// reassemble the address without it appearing
	// verbatim in the HTML source.
	reWDEmailBlock = regexp.MustCompile(`(?is)\[\[email\]\](.*?)\[\[/email\]\]`)
	reWDEmailTag   = regexp.MustCompile(`(?i)\[\[email\s+([^\s\]]+@[^\s\]]+)\]\]`)
	reWDHTMLBlock  = regexp.MustCompile(`(?s)(<(?:pre|table|ul|ol|blockquote|div|details|summary)\b.*?</(?:pre|table|ul|ol|blockquote|div|details|summary)>)`)

	// reWDLineContinuation matches `X _\nY` and captures `X` —
	// the trailing ` _` and the newline get stripped in the
	// rewrite, so the next line joins onto the current line.
	// Wikidot uses this for list items:
	//   * 事项1 _\n另一行   →   * 事项1 另一行
	// Table cells (|| ... _\n ... ||) would also benefit but
	// require a multi-line cell aware table-row pass; left
	// for a future round.
	// The `(?m)` multi-line flag is essential — the `^` /
	// `$` anchors must match per-line, not just the start /
	// end of the whole document.
	reWDLineContinuation = regexp.MustCompile(`(?m)^([^\n]*[^\s]) _\r?\n`)

	// ── Smart punctuation (Stage 2 + Stage 4 additions) ─────────
	// Wikidot recognises the following typographic pairs /
	// shorthands. We substitute the unicode form so the
	// rendered output is "smart" even when the source
	// uses straight ASCII. Order matters: longer matches
	// first (so `...` is consumed before any single `.`,
	// ` -- ` before any single `-`, the German quote
	// `,,x''` before the generic `''`).
	//
	//   ``        →   "     (left double quote, U+201C)
	//   ''        →   "     (right double quote, U+201D)
	//   `         →   ‘     (left single quote, U+2018)
	//   '         →   ’     (right single quote, U+2019)  *
	//   <<        →   «     (left angle quote, U+00AB)
	//   >>        →   »     (right angle quote, U+00BB)
	//   ...       →   …     (ellipsis, U+2026)
	//   --        →   —     (em dash, U+2014)
	//
	// Stage 4 additions (gaps closed vs the spec):
	//
	//   ,,' ,     →   „"   (German low-9 quote, single
	//                         form: opening low-9, closing
	//                         high-left used as closer)
	//   ' ,       →   ''   (placeholder — already handled
	//                         by LSQuote/RSQuote above)
	//   >>引号<<  →   »引号« (reverse guillemets for nested
	//                         quoting on the opposite side)
	//
	// The em-dash rule needs WHITESPACE on both sides —
	// `--` inside a word (e.g. "x--y") is left alone to
	// avoid turning every double-hyphen in user prose
	// into a typographic em-dash.
	//
	// (*) The single-quote pair only fires when the
	// closing `'` is preceded by content (it acts as
	// apostrophe in `it's`). Wikidot's spec uses `` `
	// for both open and close of double quotes. We add
	// a minimal `'` → `’` fallback for plain ASCII
	// apostrophes, which is the right default for
	// latin-script user prose.
	reWDSmartLDQuote = regexp.MustCompile("``")
	reWDSmartRDQuote = regexp.MustCompile("''")
	reWDSmartLSQuote = regexp.MustCompile("`")
	reWDSmartRSQuote = regexp.MustCompile(`(\w)'`)
	reWDSmartLAQuote = regexp.MustCompile("<<")
	reWDSmartRAQuote = regexp.MustCompile(">>")
	reWDEllipsis     = regexp.MustCompile(`\.\.\.`)
	reWDEmDash       = regexp.MustCompile(`(?:^|[\s(])--(?:[\s).,;!?]|$)`)
	// German low-9 quote pair `,,x''` → „x". Open: U+201E
	// (low-9 double). Close: U+201C (left double, repurposed
	// as German closer per the German conventions). The
	// regex matches the WHOLE pair in one shot so the
	// closing `''` doesn't fall through to the generic
	// right-double-quote rule that runs later.
	reWDSmartGerman = regexp.MustCompile(`,,([\s\S]+?)''`)
	// Reverse guillemets `>>x<<` → »x«. Used for nested
	// quoting when the outer level uses `<<x>>` and the
	// inner level needs the opposite-pair guillemets.
	// MUST run after `<<` so a `<<` that starts a reverse
	// pair isn't half-eaten.
	reWDSmartRGTQuote = regexp.MustCompile(`>>([^<\n]+?)<<`)

	// %%var%% — names are simple identifiers, not nested. Anything
	// not in the Vars map at render time is left as-is so authors
	// can see they typo'd a name (matching Wikidot's behaviour of
	// rendering unknown variables verbatim).
	reWDVar = regexp.MustCompile(`%%([A-Za-z_][A-Za-z0-9_]*)%%`)

	// Footnote DEFINITION lines: `^[ \t]*[N] content` (line-leading
	// only — inline [1] refs are matched separately).  The capture
	// group is the digit; the second capture is the rest of the line.
	reWDFootnoteDef = regexp.MustCompile(`(?m)^\s*\[(\d+)\]\s+(.+?)\s*$`)

	// Footnote REFERENCE in body text: `[N]` (digits only, anywhere
	// inline). We constrain to bare numbers so the URL bracket
	// syntax `[https://...]` and the email `[mailto:...]` aren't
	// confused.
	reWDFootnoteRef = regexp.MustCompile(`\[(\d+)\]`)

	// [[footnote]]text[[/footnote]] — block-form footnote
	// (Wikidot spec §footnotes). Each block-form instance
	// auto-numbers starting at 1 (or after the highest
	// already-collected inline `[N]` definition), and the
	// rendered output is a `<sup>` reference back-link to
	// the matching entry in the rendered `<ol
	// class="footnotes">` appended in Phase 13.
	reWDFootnoteBlock = regexp.MustCompile(`(?is)\[\[footnote\]\](.*?)\[\[/footnote\]\]`)

	// [[footnoteblock]] and [[footnoteblock title="..."]] —
	// single-tag form that suppresses the rendered
	// `<ol class="footnotes">` at the bottom of the
	// document (Wikidot uses this for articles where the
	// footnote list is rendered elsewhere, e.g. on a
	// nav page). The optional `title="..."` attribute
	// replaces the default "脚注:" label.
	reWDFootnoteBlockTag = regexp.MustCompile(`(?is)\[\[footnoteblock((?:\s+[a-zA-Z][\w-]*\s*=\s*"[^"]*")*)\s*\]\]`)
)

// ── Size / color lookup tables ─────────────────────────────────────────

var sizeMap = map[string]string{
	"xx-small": "0.5rem", "x-small": "0.625rem", "smaller": "0.75rem",
	"small": "0.8rem", "medium": "1rem", "large": "1.25rem",
	"x-large": "1.5rem", "xx-large": "2rem", "larger": "2.5rem",
}

var colorNames = map[string]string{
	"red": "#e74c3c", "green": "#27ae60", "blue": "#3498db",
	"yellow": "#f1c40f", "orange": "#e67e22", "purple": "#9b59b6",
	"pink": "#e91e63", "gray": "#7f8c8d", "grey": "#7f8c8d",
	"black": "#2c3e50", "white": "#ecf0f1", "cyan": "#00bcd4",
	"teal": "#009688", "indigo": "#3f51b5",
}

var admonitionTitles = map[string]string{
	"note":    "📝 注意",
	"warning": "⚠️ 警告",
	"danger":  "🚫 危险",
	"info":    "ℹ️ 信息",
	"tip":     "💡 提示",
}

// ── Render context (the dynamic side-channel) ─────────────────────────

// RenderContext carries the per-render side-channel state the wikidot
// parser needs for dynamic constructs. A nil context is fine for
// purely static content — include/module/toc/var all degrade to their
// raw source in that case (matching the pre-existing behaviour).
type RenderContext struct {
	// PageLookup resolves `[[include slug]]`, `[[module ListPages]]`,
	// and `RandomPage` to actual article rows. The production
	// implementation in internal/api/articles.go queries MySQL;
	// tests can pass a stub.
	PageLookup PageLookup

	// UserLookup resolves `[[user Name]]` and `[[*user Name]]`
	// mentions to actual user rows so the rendered link can
	// show the user's nickname / avatar / staff badge. When
	// the lookup fails (no context, no adapter, or unknown
	// name) the renderer falls back to a plain `@Name` link,
	// matching the pre-existing behaviour before the lookup
	// was wired in.
	UserLookup UserLookup

	// Vars feeds `%%name%%` substitutions. The article renderer
	// populates this with the current article, current user, rating,
	// etc. before each render.
	Vars map[string]string

	// ArticleType is the type of the article currently being rendered
	// ("wikidot" / "md" / etc.). Used as the default atype for
	// `[[include slug-without-type]]` lookups; without it, an
	// ambiguous include is rejected.
	ArticleType string
}

// PageLookup is the minimal interface the parser needs to resolve
// dynamic wikidot constructs. The production adapter (in
// internal/api/articles.go) wraps the MySQL queries against the
// articles table; tests provide an in-memory stub.
type PageLookup interface {
	// IncludeBySlug returns the article matching (atype, slug), or
	// nil if no such article exists. atype defaults to the current
	// article's type when empty.
	IncludeBySlug(atype, slug string) *IncludedPage

	// ListPages returns up to `limit` articles matching `category`,
	// ordered by `order`. category="*" means no category filter.
	// order is one of the whitelist the module syntax supports
	// (e.g. "created_at desc", "title", "updated_at desc").
	ListPages(category string, limit int, order string) []ListPageEntry

	// RandomPage returns one random article in `category` (or any if
	// "*"), or nil if the set is empty.
	RandomPage(category string) *ListPageEntry
}

// IncludedPage is what PageLookup.IncludeBySlug returns when an include
// target is found. Content is the raw source — the parser recurses
// into RenderWikidotCtx (or the matching renderer for the target's
// Type) so the embedded page's wikidot/markdown/bbcode syntax is
// fully expanded.
type IncludedPage struct {
	Type    string // "wikidot" / "md" / "bbcode" / "html"
	Content string // raw source
	Title   string
}

// ListPageEntry is the minimal row the wikidot module template needs.
// We don't reuse content.Article directly to keep the parser package
// from depending on the full row schema (which would create an
// import cycle through the content package).
type ListPageEntry struct {
	Slug           string
	Title          string
	AuthorName     string
	AuthorNickname string
	CreatedAt      time.Time
	Tags           []string
	Rating         float64
}

// UserLookup is the interface the wikidot renderer needs to
// resolve `[[user Name]]` / `[[*user Name]]` mentions. The
// production adapter (in internal/api) queries MySQL; tests
// can pass an in-memory stub. A nil lookup (or a lookup that
// returns nil for a name) degrades to the plain
// `@<Name>` link with no avatar — the same output the
// pre-UserLookup era rendered.
type UserLookup interface {
	// UserByName returns the user matching `name` (case-
	// insensitive on the canonical `username` column),
	// or nil if no such user exists. The renderer does
	// NOT pre-slug the name; the lookup is free to do
	// its own normalisation. The returned profile's
	// fields are all optional (zero-valued means "no
	// avatar / no nickname / no staff badge").
	UserByName(name string) *UserProfile
}

// UserProfile is what UserLookup.UserByName returns. Only
// the fields the renderer actually consumes are populated;
// the production adapter picks them from the users table
// row directly. Username is the canonical lowercased
// login name (what the user typed with whatever casing
// gets stored as `username` in the DB); Nickname is the
// display name (often the same as Username when the
// author didn't set a separate nickname); AvatarURL is
// the absolute URL to the avatar image (or empty when
// the user hasn't uploaded one); IsStaff is the staff /
// admin flag the front-end uses to draw the badge.
type UserProfile struct {
	ID        int64
	Username  string
	Nickname  string
	AvatarURL string
	IsStaff   bool
}

// RenderWikidot converts Wikidot markup source to HTML. Calls
// RenderWikidotCtx with a nil context — dynamic constructs (include,
// module, var) degrade to their raw source.
func RenderWikidot(source string) string {
	return RenderWikidotCtx(nil, source)
}

// RenderWikidotCtx converts Wikidot markup source to HTML using the
// provided context for dynamic constructs (include, module, var,
// footnote definitions across pages). ctx may be nil.
func RenderWikidotCtx(ctx *RenderContext, source string) string {
	if source == "" {
		return ""
	}
	p := wpGet()
	defer wpPut(p)
	p.ctx = ctx
	if ctx != nil {
		p.vars = ctx.Vars
	} else {
		p.vars = nil
	}
	return p.convert(source)
}

// ── WikidotParser (singleton, pooled for thread safety) ────────────────

type wikidotParser struct {
	blocks  map[string]string
	counter int
	ctx     *RenderContext
	vars    map[string]string
	// footnoteDefs maps the footnote number (as string) to its raw
	// text. Populated by Phase 0 (pre-parse scan) and by the
	// [[footnote]] block-form handler in Phase 1q, consumed
	// by Phase 13 (post-render append). We stash the original
	// definition line as a placeholder so the inline pass doesn't
	// mis-treat the `[N]` as a body reference.
	footnoteDefs map[string]string
	// footnoteSuppressed is set true when a `[[footnoteblock]]`
	// marker is encountered. Phase 13 honours this and
	// skips the rendered `<ol class="footnotes">` append.
	footnoteSuppressed bool
	// footnoteTitle is the (optional) `title="..."` value
	// from the `[[footnoteblock]]` marker. Empty string
	// means "use the default 脚注: label".
	footnoteTitle string
	// footnoteBlockNums tracks the per-render auto-numbered
	// index for [[footnote]] block-form. Starts at 1 and
	// increments for each block-form instance; the
	// matching `[^N]` reference is emitted at the call
	// site and the entry is added to footnoteDefs under
	// the same N.
	footnoteBlockNums int
	// headingSeq is the running counter for heading anchor ids
	// (e.g. "h2-1", "h2-2", "h3-1"). Reset per render.
	headingSeq int
	// headings collects the (level, id, text) tuples that [[toc]]
	// walks over to build the table of contents.
	headings []headingEntry
}

type headingEntry struct {
	Level int
	ID    string
	Text  string
	// SkipTOC is true for headings introduced via the `+*`
	// prefix. The anchor id is still emitted (so
	// `[#name text]` / `[[[page#anchor]]]` can target
	// the heading), but the TOC builder omits these
	// entries from its rendered list.
	SkipTOC bool
}

var wpPool = sync.Pool{New: func() any { return &wikidotParser{} }}

func wpGet() *wikidotParser { return wpPool.Get().(*wikidotParser) }
func wpPut(p *wikidotParser) {
	p.blocks = nil
	p.counter = 0
	p.ctx = nil
	p.vars = nil
	p.footnoteDefs = nil
	p.footnoteSuppressed = false
	p.footnoteTitle = ""
	p.footnoteBlockNums = 0
	p.headingSeq = 0
	p.headings = nil
	wpPool.Put(p)
}

func (p *wikidotParser) storeBlock(html string) string {
	p.counter++
	key := fmt.Sprintf("%%BLOCK_%d%%", p.counter)
	if p.blocks == nil {
		p.blocks = make(map[string]string)
	}
	p.blocks[key] = html
	return key
}

func (p *wikidotParser) convert(source string) string {
	return p.convertInternal(source, true)
}

// convertNoFootnote is the same as convert but skips the final
// footnote-list append. Internal recursive calls (e.g. nested
// `[[div ...]]` blocks recursing into their inner content) use
// this so a wikidot page that has both `[[div ...]]` and
// `[[footnote]]` style references doesn't end up with N
// footnote lists (one per nested div level).
func (p *wikidotParser) convertNoFootnote(source string) string {
	return p.convertInternal(source, false)
}

func (p *wikidotParser) convertInternal(source string, appendFootnotes bool) string {
	out := source

	// Shadow the vars map so inlineOnly callbacks (which don't
	// have a parser pointer) can read it. The shadow is
	// per-render — concurrent renders don't share state, but
	// each render's pipeline sees a stable table.
	p.setInlineVars()
	defer p.clearInlineVars()

	// ── Phase 0: pre-scan for footnote definitions ──────────────────
	//
	// Wikidot footnote semantics: `[1] foo` lines (when they appear
	// as standalone line-leading text, typically under a "脚注:"
	// header at the bottom of the article) are the definition
	// lines. Inline `[1]` in body paragraphs is a reference.
	//
	// We collect the defs into a map AND replace each def line
	// with a placeholder marker. The inline pass later sees
	// placeholder markers instead of `[N]`, so it doesn't
	// double-convert definitions into body references. The marker
	// is restored in Phase 10 with a follow-up "ignored" comment
	// so a reader can see that a def was consumed.
	out = p.collectFootnoteDefs(out)

	// ── Phase 0.5: %%var%% substitution ────────────────────────────
	out = p.replaceVars(out)

	// ── Phase 0.6: literal escape @@...@@ ─────────────────────────
	// Runs AFTER %%var%% (so vars are still substituted
	// outside `@@...@@`) and AFTER the var pass, but BEFORE
	// Phase 1's block-stash pass — that way `[[code]]`, div
	// tags, and every other block construct inside `@@...@@`
	// is left alone (the literal construct protects the
	// inner text from any further interpretation).
	out = reWDLiteral.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDLiteral.FindStringSubmatch(s)
		// HTML-escape the inner text BEFORE stashing so the
		// restored block is safe to drop into a paragraph
		// without re-running the entity-encoding pass.
		return p.storeBlock(html.EscapeString(m[1]))
	})

	// ── Phase 0.65: HTML comments [!-- ... --] ────────────────────
	// Drop entirely. Comments are stripped (Wikidot's own
	// behaviour) — even `[!-- visible --]` doesn't render its
	// body. We run this BEFORE every other inline pass so the
	// comment body (which may contain `**`, `//`, `[[ ]]` that
	// would otherwise feed the inline stack) is dropped in a
	// single shot. The non-greedy match across newlines handles
	// multi-line comments like `[!--\n\nfoo\n--]`.
	out = reWDComment.ReplaceAllString(out, "")

	// ── Phase 1: block storage ─────────────────────────────────────
	// The order matters: anything that can contain `]]` later in the
	// content (math, code, div, html) has to be stashed before patterns
	// that match raw HTML, otherwise the placeholder markers would be
	// wrapped by `<p>`.

	// 1a. [[html]] — kept raw on purpose; this is how we refuse the
	// "let authors paste iframe embeds" footgun. Render the content
	// inside an escaped <pre> so an admin can still see what the
	// source actually said (and rip out the wrapper if they really
	// want it).
	out = reWDHTMLRaw.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDHTMLRaw.FindStringSubmatch(s)
		return p.storeBlock(fmt.Sprintf(`<pre class="wikidot-html-escaped">&lt;html&gt;\n%s\n&lt;/html&gt;</pre>`, html.EscapeString(strings.TrimSpace(m[1]))))
	})

	// 1b. Math — keep LaTeX source verbatim, wrap in delimiters so a
	// future MathJax/KaTeX script can replace them client-side.
	out = reWDMath.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDMath.FindStringSubmatch(s)
		return p.storeBlock(fmt.Sprintf(`<div class="wikidot-math">\(%s\)</div>`, strings.TrimSpace(m[1])))
	})

	// 1c. [[code]] blocks
	out = reWDCode.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCode.FindStringSubmatch(s)
		return p.storeBlock(renderCodeBlock(m[2], m[1]))
	})

	// 1d. Collapsible sections. The attribute tail (group
	// 1) is parsed by reWDCollapsibleAttr. Recognised
	// keys:
	//   - show="..."   summary text when collapsed
	//   - hide="..."   summary text when expanded
	//   - folded="no"  render with the <details open>
	//                   attribute so the body is shown
	//                   by default
	//   - hideLocation="both"  render an extra "hide"
	//                   affordance at the bottom of the
	//                   body (the default is to render
	//                   only at the top, like Wikidot)
	// Unknown keys are silently dropped.
	out = reWDCollapsible.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCollapsible.FindStringSubmatch(s)
		attrs := parseCollapsibleAttrs(m[1])
		show := attrs["show"]
		if show == "" {
			show = "+ show block"
		}
		hide := attrs["hide"]
		if hide == "" {
			hide = "- hide block"
		}
		open := ""
		if strings.EqualFold(attrs["folded"], "no") {
			open = " open"
		}
		inner := p.convertNoFootnote(m[2])
		// The summary element normally holds just the
		// "show" / "hide" text. When hideLocation="both"
		// we add a second summary at the bottom so the
		// reader can collapse the block from either end.
		// We use a class on the bottom summary so the
		// CSS can hide it for the default ("top"-only)
		// case without breaking either behaviour.
		hideLoc := strings.ToLower(attrs["hidelocation"])
		bottom := ""
		if hideLoc == "both" {
			bottom = fmt.Sprintf(`<summary class="wiki-collapsible-bottom">%s</summary>`, hide)
		}
		return p.storeBlock(fmt.Sprintf(`<details class="wiki-collapsible"%s><summary>%s</summary><div class="collapsible-content">%s</div>%s</details>`, open, show, inner, bottom))
	})

	// 1d.5 Line continuation — runs after the block-level
	// stash phases (`[[code]]`, `[[collapsible]]`, etc.)
	// but BEFORE the table-row pass (1e) and the inline
	// stack.
	//
	// A line ending in ` _` (space + underscore) is
	// joined onto the next line with a single space —
	// this matches Wikidot's behaviour for list items
	// (`* 事项1 _\n另一行` → `* 事项1 另一行`).
	//
	// Table cells are NOT folded by this pass; the
	// `||超长 _\n内容 8||` form (which Wikidot renders
	// as a multi-line cell with a soft break) would
	// require the table-row pass to accept multi-line
	// cells via `<br />`, which the current regex
	// doesn't support. We log a TODO rather than
	// silently break the cell layout.
	//
	// The regex is `^X _\n` (multi-line): the line
	// ending in ` _` (space + underscore) is joined
	// onto the next line with a space. Subsequent
	// `[[code]]`-protected blocks are already
	// `%%BLOCK_N%%` placeholders so the regex can't
	// accidentally fold lines inside one.
	out = reWDLineContinuation.ReplaceAllString(out, "$1 ")

	// 1d.7 Smart punctuation — runs after the block
	// stash (so `[[code]]` content is already a
	// placeholder) but BEFORE the inline stack (so a
	// smart-quote `'` doesn't get re-interpreted by the
	// italic regex as a markdown emphasis). The em-dash
	// rule uses a non-greedy whitespace check so we
	// don't replace `--` inside a word.
	//
	// Order matters within this block. Pairs run BEFORE
	// their singleton halves: German `,,…''` consumes
	// the closing `''` so the generic `'' → "`
	// rule doesn't double-fire on the German close;
	// double-quote `` `` … '' `` likewise consumes its
	// own `` `` opener and '' `` closer first.
	out = reWDSmartGerman.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSmartGerman.FindStringSubmatch(s)
		return "\u201e" + m[1] + "\u201c"
	})
	out = reWDSmartLDQuote.ReplaceAllString(out, "\u201c")
	out = reWDSmartRDQuote.ReplaceAllString(out, "\u201d")
	out = reWDSmartLSQuote.ReplaceAllString(out, "\u2018")
	out = reWDSmartRSQuote.ReplaceAllString(out, "$1\u2019")
	out = reWDSmartLAQuote.ReplaceAllString(out, "\u00ab")
	// Reverse guillemets run AFTER the forward `<<` rule
	// so a `<<x<<` outer-pair isn't half-eaten. The
	// closing `<<` is consumed here; a literal `<<` that
	// doesn't pair with a preceding `>>` is left as-is.
	out = reWDSmartRGTQuote.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSmartRGTQuote.FindStringSubmatch(s)
		return "\u00bb" + m[1] + "\u00ab"
	})
	out = reWDSmartRAQuote.ReplaceAllString(out, "\u00bb")
	out = reWDEllipsis.ReplaceAllString(out, "\u2026")
	out = reWDEmDash.ReplaceAllString(out, "$1\u2014$2")

	// 1e. Row-based tables (`|| ... ||` lines, contiguous group). Build
	// the table HTML and stash it so subsequent regex passes don't try
	// to interpret `|` characters or re-parse the cell content.
	//
	// 1e.0  First, stitch together cells that span multiple lines
	// (Wikidot's `_` continuation marker inside a `||…||` block).
	// Without this pre-pass, a row like
	//   `|||| 超长 _\n  内容 8||`
	// would not match the `^||…||$` line regex (the first line
	// doesn't end with `||`) and would fall through as plain
	// prose. After this pass, the multi-line cell joins into a
	// single space-separated cell so the row regex picks it up.
	out = joinMultiLineTableRows(out)
	out = renderWikidotTableRows(p, out)
	out = renderWikidotTableRows(p, out)

	// 1f. [[table]]...[[/table]] block syntax
	out = reWDTable.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDTable.FindStringSubmatch(s)
		return p.storeBlock(renderWikidotTable(p, m[1]))
	})

	// 1f.5 [[gallery]]...[[/gallery]] image-gallery block. Each
	// body line is either a bare URL (`https://…/img.jpg`) or
	// `URL | caption` (URL pipe space caption). Lines are
	// individually sanitised through sanitizeURLForAttr; an
	// unsafe line is dropped (the comment author can't easily
	// crash the page with a malformed URL, and the rendered
	// gallery doesn't change shape when one line is dropped).
	//
	// Output wraps each image in a `<figure>` with an optional
	// `<figcaption>`; the outer `<div class="wikidot-gallery">`
	// is the grid hook the front-end uses to lay the figures
	// out as a thumbnail grid.
	out = reWDGallery.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDGallery.FindStringSubmatch(s)
		body := m[1]
		var sb strings.Builder
		sb.WriteString(`<div class="wikidot-gallery">`)
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Split on the FIRST `|` so captions that
			// contain a pipe are preserved as-is — only
			// one caption pipe is supported (the second
			// pipe, if any, becomes part of the caption).
			parts := strings.SplitN(line, "|", 2)
			url := strings.TrimSpace(parts[0])
			caption := ""
			if len(parts) == 2 {
				caption = strings.TrimSpace(parts[1])
			}
			safe := sanitizeURLForAttr(url)
			if safe == "" {
				continue
			}
			alt := html.EscapeString(caption)
			if caption != "" {
				sb.WriteString(fmt.Sprintf(
					`<figure><img src="%s" alt="%s" loading="lazy"><figcaption>%s</figcaption></figure>`,
					safe, alt, alt,
				))
			} else {
				sb.WriteString(fmt.Sprintf(
					`<figure><img src="%s" alt="" loading="lazy"></figure>`,
					safe,
				))
			}
		}
		sb.WriteString(`</div>`)
		return p.storeBlock(sb.String())
	})

	// 1f.6 [[form ...]]…[[/form]] — paired form block. The
	// inner body is run through inlineOnly (so links /
	// wikidot-inline-formatting survive) AND through the
	// form-widget substitution pass that turns [[input
	// …]] / [[textarea …]]content[[/textarea]] / [[button
	// …]] / [[checkbox …]] / [[radio …]] / [[select…]] /
	// [[option…]]label[[/option]] into the corresponding
	// HTML form controls. We use the depth-counting scanner
	// so a nested [[form]] inside (rare but legal) is
	// balanced correctly.
	out = renderWikidotFormBlocks(out)

	// 1g. [[include SLUG | key=val | …]] — recursive page embed.
	// Resolved here, before div/float/span, so an include that
	// wraps block-level wikidot syntax in its target page is
	// already HTML by the time the paragraph-wrapper runs. A
	// missing PageLookup or unknown slug degrades to the raw
	// source (matching Wikidot's "leave the markup visible" fallback).
	out = reWDInclude.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDInclude.FindStringSubmatch(s)
		slug := strings.TrimSpace(m[1])
		attrs := parseIncludeAttrs(m[2])
		target := p.lookupInclude(slug, attrs)
		if target == nil {
			// Leave the original markup intact so the author can
			// see and fix the missing slug. We DON'T stash as a
			// block — the raw text is fine for visibility.
			return s
		}
		return p.storeBlock(p.renderInclude(target, attrs))
	})

	// 1h. [[module Name attrs]]…[[/module]] — block module.
	// Paired matching so the inner template survives into the
	// block resolver. The inner template can contain any wikidot
	// syntax (e.g. `* %%title%%` for ListPages list rendering),
	// so we re-run the whole pipeline on it after the per-page
	// %%-substitution.
	out = p.renderModules(out)

	// 1i. [[div class=…]] and [[div style=…]] — deepest-first so
	// nested divs don't have their inner [[div …]] close the
	// outer [[/div]] early. Regex non-greedy matching can't
	// handle balanced nesting on its own; we use a manual
	// stack-based scanner that finds each pair by counting
	// [[div ...]] and [[/div]] occurrences from each open tag.
	out = p.renderDivBlocks(out)

	// 1j. [[float=left|right]] — wrap content in a floating div
	out = reWDDivFloat.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDivFloat.FindStringSubmatch(s)
		inner := p.convertNoFootnote(m[2])
		side := strings.ToLower(strings.TrimSpace(m[1]))
		if side != "left" && side != "right" {
			side = "left"
		}
		return p.storeBlock(fmt.Sprintf(`<div style="float:%s">%s</div>`, side, inner))
	})

	// 1k. [[span class=…]] / [[span style=…]] — inline only, no block
	// patterns or paragraph wrapping (span is inline; nesting <p> inside
	// it is invalid HTML).
	//
	// The class-form uses a balanced matcher (renderBalancedSpanClass
	// below) so nested `[[span class="ruby"]]...[[span class="rt"]]...
	// [[/span]][[/span]]` constructs produce valid HTML5 `<ruby>` /
	// `<rt>` elements, instead of the previous single-regex pass that
	// emitted generic `<span class="ruby">` with the inner rt/text
	// leak through verbatim. See renderBalancedSpanClass for the
	// fixed-point loop strategy.
	out = renderBalancedSpanClass(out)
	out = reWDSpanStyle.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDSpanStyle.FindStringSubmatch(s)
		inner := inlineOnly(m[2])
		if css := sanitizeCSSValue(m[1]); css != "" {
			return fmt.Sprintf(`<span style="%s">%s</span>`, css, inner)
		}
		return inner
	})

	// 1l. [[size]] / [[color]]
	out = renderWikidotSizeBlocks(out)
	out = reWDColor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDColor.FindStringSubmatch(s)
		css := m[1]
		if v, ok := colorNames[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, inlineOnly(m[2]))
	})

	// 1k.5 Background colour `[[bgcolor name]]…[[/bgcolor]]`.
	// Companion to color; same name table + CSS pass-through.
	// Output is a span with `background:` style (block-level
	// padding would need a div, but Wikidot treats bgcolor
	// as inline so the wrapped text inherits the colour
	// without breaking the surrounding paragraph).
	out = reWDBgcolor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDBgcolor.FindStringSubmatch(s)
		css := m[1]
		if v, ok := colorNames[strings.ToLower(css)]; ok {
			css = v
		} else if css = sanitizeCSSValue(css); css == "" {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="background:%s">%s</span>`, css, inlineOnly(m[2]))
	})

	// 1k.6 Font family `[[font F]]…[[/font]]`. CSS value
	// sanitised (drops `()`, `{}`, `expression`, etc.) so
	// an attacker can't slip a `font-family: expression(...)`
	// payload into the article.
	out = reWDFont.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDFont.FindStringSubmatch(s)
		css := sanitizeCSSValue(m[1])
		if css == "" {
			return inlineOnly(m[2])
		}
		return fmt.Sprintf(`<span style="font-family:%s">%s</span>`, css, inlineOnly(m[2]))
	})

	// 1k.7 Indent block `[[indent]]…[[/indent]]`. Renders
	// to `<div class="wikidot-indent">` so CSS controls
	// the depth (and dark-mode-aware contrast). The body
	// uses `inlineOnly` (NOT `convertNoFootnote`) — same
	// reasoning as the [[note]] block: routing through
	// the full convert pipeline would emit a `<p>` /
	// `<br />` inside the indent div that the downstream
	// paragraph-wrap then has to deal with, producing
	// invalid `<p><div><p>...</p></div></p>` HTML. With
	// inlineOnly + newline→`<br />`, the body becomes a
	// flat inline run that sits cleanly inside the
	// block-level `<div>`. Nested indents accumulate
	// padding via CSS — each level adds another wrapping
	// `<div class="wikidot-indent">`.
	//
	// Nested indents need a BALANCED matcher: a non-greedy
	// regex would consume an INNER `[[/indent]]` first,
	// leaving the outer close un-balanced. We do a small
	// depth-counting scan via `renderWikidotIndentBlocks`
	// that finds each matching close in turn, then replace
	// the whole span in one pass.
	out = renderWikidotIndentBlocks(out, p)

	// 1k.8 Iframe / video / audio. URL sanitised the same
	// way images are — protocol allowlist + quote-reject
	// + no protocol-relative tricks. Width / height are
	// optional and fall back to the renderer's defaults
	// (defined here so the regex and the renderer stay
	// in sync).
	out = reWDIframe.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDIframe.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		w := m[2]
		h := m[3]
		if w == "" {
			w = "100%"
		}
		if h == "" {
			h = "400"
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<iframe src="%s" width="%s" height="%s" loading="lazy" frameborder="0"></iframe>`, safe, html.EscapeString(w), html.EscapeString(h))
		}
		return ""
	})
	out = reWDVideo.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDVideo.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		w := m[2]
		h := m[3]
		if w == "" {
			w = "100%"
		}
		if h == "" {
			h = "auto"
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<video src="%s" width="%s" height="%s" controls preload="metadata"></video>`, safe, html.EscapeString(w), html.EscapeString(h))
		}
		return ""
	})
	out = reWDAudio.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAudio.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<audio src="%s" controls preload="metadata"></audio>`, safe)
		}
		return ""
	})

	// 1k.9 `[[date]]` / `[[date format]]`. Substitutes the
	// current server time formatted per `format` (Go
	// time format string). Empty / invalid format falls
	// back to the site default `2006-01-02`. The
	// rendered date is intentionally the render-time
	// value — articles that want a stable date should
	// hard-code it (Wikidot's own behaviour).
	out = reWDDate.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDDate.FindStringSubmatch(s)
		format := strings.TrimSpace(m[1])
		if format == "" {
			format = "2006-01-02"
		}
		// Catch the most common format mistakes
		// (Wikidot uses its own token set — e.g.
		// `$YYYY-$MM-$DD`) so a migrated article
		// doesn't silently render garbage. We
		// accept any string containing `$` and treat
		// it as a Wikidot-style format, mapping the
		// documented tokens to Go's layout; anything
		// else passes through as a Go format.
		format = mapWikidotDateFormat(format)
		return html.EscapeString(time.Now().Format(format))
	})

	// 1k.10 `[[tabview]]…[[/tabview]]` — Wikidot's
	// tabbed UI. The outer block contains a sequence
	// of `[[tab TITLE]]…[[/tab]]` children, each
	// becoming a tab button + content panel. Output
	// is a `.wikidot-tabview` container with a nav
	// list (`.wikidot-tab-nav`) and a panels stack
	// (`.wikidot-tab-panels`); a small client-side
	// script (see web/components/ArticleView.tsx)
	// wires the clicks. We use balanced matching
	// because nested `[[tabview]]` inside a tab
	// (rare but legal) should not break the outer
	// match — same depth-counting trick as the
	// indent block.
	out = renderWikidotTabviews(out, p)

	// 1m. Alignment blocks ([[=]] / [[<]] / [[>]] / [[==]])
	out = reWDCenter.ReplaceAllString(out, `<div style="text-align:center">$1</div>`)
	out = reWDLeftBlock.ReplaceAllString(out, `<div style="text-align:left">$1</div>`)
	out = reWDRight.ReplaceAllString(out, `<div style="text-align:right">$1</div>`)
	out = reWDJustify.ReplaceAllString(out, `<div style="text-align:justify">$1</div>`)

	// 1m.5 Single-line alignment shortcuts. Runs after the
	// block forms so a `[[<]]` opener on the same line as
	// the inline shortcut can't be confused for one. The
	// regex matches `=` / `<` followed by a space at the
	// START of a line, so inline mentions of `=` (e.g.
	// `x = y`) are not promoted into alignment divs.
	out = reWDCenterLine.ReplaceAllString(out, `<div style="text-align:center">$1</div>`)
	out = reWDLeftLine.ReplaceAllString(out, `<div style="text-align:left">$1</div>`)

	// 1n. [[youtube ID]] — emit a placeholder div carrying the ID as
	// a data attribute. The iframe itself is NOT emitted here because
	// the ArticleView client passes rendered HTML through DOMPurify,
	// which forbids <iframe> outright to keep untrusted author markup
	// from pulling in arbitrary third-party pages. ArticleView has a
	// small post-DOMPurify pass that swaps these placeholders for the
	// real <iframe>, with the ID validated to a safe URL path segment
	// by the client.
	out = reWDYoutube.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDYoutube.FindStringSubmatch(s)
		id := strings.TrimSpace(m[1])
		return p.storeBlock(fmt.Sprintf(`<div class="wikidot-youtube" data-youtube-id="%s"></div>`, html.EscapeString(id)))
	})

	// 1o. Paired anchor block `[[a name="x"]]content[[/a]]` runs
	// FIRST so the non-greedy `.*?` between opening and `[[/a]]`
	// closing can capture the inner content before the simpler
	// self-closing `reWDAnchorDef` below eats just the opening tag.
	//
	// We emit the id-bearing span as an empty placeholder *before* the
	// (recursively converted) content rather than wrapping content in
	// the span. Wrapping would nest <p> (from paragraph wrapping) inside
	// the inline <span>, which is invalid HTML and ends up visually
	// broken in browsers. Anchoring an empty span right above the
	// content still gives the jump-link `[[#x text]]` the correct
	// scroll target.
	out = reWDAnchorPair.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchorPair.FindStringSubmatch(s)
		id := html.EscapeString(strings.TrimSpace(m[1]))
		inner := p.convertNoFootnote(m[2])
		return p.storeBlock(fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id)) + inner
	})

	// 1o2. Self-closing `[[a name="…"]]` (no `[[/a]]`). Stored as a
	// placeholder so the regex that resolves [#name] jumps later
	// doesn't try to also fire on this line.
	out = reWDAnchorDef.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchorDef.FindStringSubmatch(s)
		// Anchor IDs are commonly Chinese or other non-ASCII; fall
		// back to a permissive escape rather than dropping the
		// anchor entirely. sanitizeAnchorID is too strict for IDs
		// the author genuinely wants to use.
		id := strings.TrimSpace(m[1])
		id = html.EscapeString(id)
		return p.storeBlock(fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id))
	})

	// 1o2.5 `[[# name]]` — compact anchor-def form. Same output
	// shape as `[[a name="name"]]` (a `<span id="…">` with the
	// `wiki-anchor` class). The id rule is the same
	// (HTML-escape; non-ASCII preserved).
	out = reWDHashAnchorDef.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDHashAnchorDef.FindStringSubmatch(s)
		id := html.EscapeString(strings.TrimSpace(m[1]))
		return p.storeBlock(fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, id))
	})

	// 1p. [[toc]] — keep the marker in place; Phase 12 will swap it
	// for a real <ul> built from the headings collected in Phase 4.
	// We store the marker as a block placeholder so paragraph-wrap
	// doesn't try to wrap it in <p>. Variants `[[f<toc]]` /
	// `[[f>toc]]` are floated (left / right) — see renderTOC.
	out = reWDTOC.ReplaceAllStringFunc(out, func(s string) string {
		return p.storeBlock("[[__TOC__]]")
	})
	out = reWDFLTOC.ReplaceAllStringFunc(out, func(s string) string {
		return p.storeBlock("[[__FTOC_LEFT__]]")
	})
	out = reWDFRTOC.ReplaceAllStringFunc(out, func(s string) string {
		return p.storeBlock("[[__FTOC_RIGHT__]]")
	})

	// 1p.5 [[footnote]]text[[/footnote]] — block-form footnote.
	// Each block auto-numbers starting after the highest
	// already-collected inline `[N]` definition (so an
	// article that has `[1] … [3]` definitions followed by
	// `[[footnote]]…[[/footnote]]` blocks continues the
	// sequence at 4). The reference is a `<sup>` back-link
	// in-place of the `[[footnote]]` token, and the
	// definition is added to `p.footnoteDefs` for the
	// Phase-13 list append. Inline wikidot markup inside
	// the footnote is processed via convertNoFootnote so
	// a nested `[[footnote]]` would never recurse (and
	// would also never be authored — Wikidot's
	// `[[footnote]]` is a leaf block).
	out = reWDFootnoteBlock.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDFootnoteBlock.FindStringSubmatch(s)
		p.footnoteBlockNums++
		n := p.footnoteBlockNums
		// If inline definitions already used indices up
		// to K, push the block sequence past them so we
		// don't collide. (The pool is shared across the
		// article; inline refs / defs use the same map.)
		for {
			_, used := p.footnoteDefs[strconv.Itoa(n)]
			if !used {
				break
			}
			p.footnoteBlockNums++
			n = p.footnoteBlockNums
		}
		key := strconv.Itoa(n)
		// Store the inner text as the definition; the
		// Phase-13 list appender will pull it back out
		// and wrap it in an `<li id="fn-N">`.
		if p.footnoteDefs == nil {
			p.footnoteDefs = make(map[string]string)
		}
		p.footnoteDefs[key] = m[1]
		// Emit the in-place reference. The body of
		// the block (the actual footnote text) is
		// consumed — Wikidot's block-form footnote
		// does NOT render its text inline; only the
		// `<sup>` back-link.
		return fmt.Sprintf(`<sup class="footnote-ref"><a href="#fn-%s" id="fnref-%s">%s</a></sup>`, key, key, key)
	})

	// 1p.6 [[footnoteblock]] and [[footnoteblock title="..."]]
	// — single-tag marker. When present, the rendered
	// `<ol class="footnotes">` list at the bottom of the
	// document is suppressed (Wikidot uses this for
	// articles that share a single footnote list across
	// pages, or where the list is rendered elsewhere on
	// the page). The optional `title="..."` attribute
	// replaces the default "脚注:" label that the
	// <section class="footnotes"> would otherwise have.
	out = reWDFootnoteBlockTag.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDFootnoteBlockTag.FindStringSubmatch(s)
		attrs := parseCollapsibleAttrs(m[1])
		p.footnoteSuppressed = true
		if t, ok := attrs["title"]; ok {
			p.footnoteTitle = t
		}
		// Render the footnote list IN-PLACE at the
		// marker location. Wikidot treats this tag
		// as a position anchor for the list, so we
		// replace it with the `<section>` rather
		// than relying on the auto-append (which is
		// suppressed by footnoteSuppressed). The
		// surrounding newlines ensure the paragraph
		// wrapper sees the section as a block
		// boundary instead of an inline tail of
		// the previous paragraph.
		return "\n" + renderFootnoteSection(p.footnoteDefs, p.footnoteTitle) + "\n"
	})

	// ── Phase 1.5: horizontal rules ─────────────────────────────────
	// Run BEFORE the Phase-2 inline stack so the strikethrough
	// regex (`--(.+?)--`) doesn't eat a 3-5 dash run and turn
	// `---` / `----` into `<s>--</s>` / `<s>---</s>` instead of
	// an `<hr>`. The HR pass replaces the line with `<hr>` (block-
	// level), so the inline pass can never see the dash run.
	out = reWDHR.ReplaceAllString(out, `<hr>`)

	// `[[divider]]` — themed HR equivalent to `----` but
	// emitted with a `wikidot-divider` class so the
	// front-end can style it without touching the plain
	// `<hr>` used by the line form.
	out = reWDDivider.ReplaceAllString(out, `<hr class="wikidot-divider">`)

	// `[[note]]…[[/note]]` — inline note box. Run
	// before Phase 2 (inline formatting) so the
	// paragraph-wrapper sees a clean block boundary
	// on each side. The body goes through
	// `inlineOnly` (NOT `convertNoFootnote`) so any
	// `**bold**` / `//italic//` / `[link]` markup
	// inside the note still renders, but no `<p>`
	// wrapper or `<br />` line-breaks get added —
	// the `<aside>` itself is a block container, so
	// wrapping its body would produce invalid
	// `<aside><p>...</p></aside>` HTML with the
	// `<p>` immediately inside `<aside>` (which the
	// browser would auto-close anyway, leaving the
	// rest of the note outside any wrapper).
	out = reWDNote.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDNote.FindStringSubmatch(s)
		// Convert the body to inline HTML and then
		// collapse hard newlines into `<br />` so the
		// paragraph-wrapper downstream doesn't see
		// lines inside the `<aside>` and produce a
		// stray `<p>...</p>` inside the block. The
		// `<aside>` is itself a block container, so
		// wrapping its body in `<p>` would be
		// invalid HTML (browsers auto-close `<p>`
		// when they see a block inside, which leaves
		// the rest of the note unwrapped).
		body := strings.TrimSpace(inlineOnly(m[1]))
		body = strings.ReplaceAll(body, "\n", "<br />")
		return fmt.Sprintf(`<aside class="wikidot-note">%s</aside>`, body)
	})

	// ── Phase 2: inline formatting ─────────────────────────────────
	// Pre-process: replace backslash-escaped slashes with a sentinel
	// so `//` isn't confused with italic markers.
	out = strings.ReplaceAll(out, `\\/`, "\x00SL")

	// 2a. (no-op here — `@@...@@` is now handled in Phase 0.6
	// as a literal-escape construct. The old `@@...@@` →
	// `<code>...</code>` monospace behaviour was a Wikidot
	// spec misread; the real convention is `{{...}}` for
	// monospaced text and `@@...@@` for verbatim. The
	// Phase-0.6 stash replaces every `@@...@@` in the
	// source with a `%%BLOCK_N%%` placeholder by the time
	// we reach this point, so no inline handling is
	// required here.)

	// 2b. Inline colour `##colorname|text##` or `##hex|text##` (the
	// bare-hex form `##44FF88|text##` from the rule-wiki spec is also
	// accepted — see applyWikidotInlineColor).
	out = reWDInlineColor.ReplaceAllStringFunc(out, applyWikidotInlineColor)

	// 2c. The standard formatting stack.
	out = reWDBold.ReplaceAllString(out, `<strong>$1</strong>`)
	// Italic regex requires the opening `//` to NOT be preceded by `:` —
	// otherwise the `://` inside `https://example.com` URLs (which are
	// already wrapped by the Phase 6 auto-linker into <a> tags when the
	// list / table / span passes call inlineOnly later) trips the
	// match and inserts `<em>` in the middle of an <a href="...">.
	out = reWDItalic.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDItalic.FindStringSubmatch(s)
		return m[1] + "<em>" + m[2] + "</em>"
	})
	out = reWDUnderline.ReplaceAllString(out, `<u>$1</u>`)
	out = reWDStrikethrough.ReplaceAllString(out, `<s>$1</s>`)
	out = reWDSuperscript.ReplaceAllString(out, `<sup>$1</sup>`)
	out = reWDSubscript.ReplaceAllString(out, `<sub>$1</sub>`)
	out = reWDInlineCode.ReplaceAllString(out, `<code>$1</code>`)

	// 2d. Footnote REFERENCES in body text — `[N]` becomes a
	// back-link to the matching <li id="fn-N"> in the footer
	// list. We do this AFTER bold/italic so `**[1]**` is rendered
	// as a bold reference, not a bold raw digit. Definition
	// lines were already replaced with placeholders in Phase 0,
	// so this regex only fires on body references.
	out = reWDFootnoteRef.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDFootnoteRef.FindStringSubmatch(s)
		n := m[1]
		// Suppress the link entirely if there's no matching def —
		// a stray `[42]` shouldn't render as a broken anchor.
		if _, ok := p.footnoteDefs[n]; !ok {
			return fmt.Sprintf(`<sup class="footnote-ref-unresolved">[%s]</sup>`, html.EscapeString(n))
		}
		return fmt.Sprintf(`<sup class="footnote-ref"><a href="#fn-%s" id="fnref-%s">%s</a></sup>`, n, n, html.EscapeString(n))
	})

	out = strings.ReplaceAll(out, "\x00SL", "/")

	// ── Phase 3: links & images ─────────────────────────────────────
	// 3a. External link `[url text]` (open in new tab; rel=noopener
	// is the safe default for user-authored content).
	out = reWDExternalLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDExternalLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// NOTE: Single-bracket starred link `[*http://... text]` is
	// intentionally NOT processed here — it's pushed down to
	// Phase 3a.7 below, AFTER the triple-bracket starred /
	// wiki-link forms. Running the single-bracket starred regex
	// first would match the inner `[*http://...|Wikidot]` of
	// `[[[*http://...|Wikidot]]]`, leaving the outer triple-
	// bracket construct unparsed and producing `[[<a>...|Wikidot</a>]]`
	// instead of the intended new-tab link. So: triple-bracket
	// forms first, single-bracket forms later.

	// 3b. Mailto `[mailto:addr text]`
	out = reWDMailto.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDMailto.FindStringSubmatch(s)
		addr := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = addr
		}
		if safe := sanitizeURLForAttr("mailto:" + addr); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3c.5 Triple-bracket starred link `[[[*http://...|Text]]]`.
	// Same as `[[[http://url|Text]]]` but the `*` makes the
	// link open in a new tab. Must run BEFORE the wiki-link
	// regex (`reWDWikiLink`) so the `*` doesn't get folded
	// into a wikidot internal-page name (the wiki-link form
	// would otherwise treat `*http://...` as a page slug and
	// render `<a href="/wikidot/*http://…">…</a>`, which is
	// both visually wrong and the wrong navigation semantic).
	out = reWDStarredTripleLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDStarredTripleLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3c. Internal wiki link `[[[page]]]` / `[[[page|alias]]]`.
	// Run AFTER the starred-triple form (3c.5) so a `*[[...]]`
	// marker is consumed by the new-window branch first.
	out = reWDWikiLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		href := m[1]
		text := m[1]
		if m[2] != "" {
			text = m[2]
		}
		if !strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			href = "/wikidot/" + href
		}
		if safe := sanitizeURLForAttr(href); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3c.6 Relative-path single-bracket link `[/path text]`.
	// Short-form for wikidot-internal navigation URLs that
	// don't need an explicit `http://site.wikidot.com/`
	// prefix. The path is preserved verbatim (the
	// `sanitizeURLForAttr` allow-list covers internal `/...`
	// paths).
	out = reWDRelativeLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDRelativeLink.FindStringSubmatch(s)
		href := m[1]
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = href
		}
		if safe := sanitizeURLForAttr(href); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3c.7 Wikidot "empty placeholder" link `[# display]`.
	// A leading space after `#` discriminates this form
	// from the real anchor jump-link (`[#name text]`).
	// Rendered as a normal-looking link whose click does
	// nothing (`href="javascript:;"` per the wikidot
	// spec — and so scoped by the CSP-friendly
	// sanitizeURLForAttr allow-list).
	out = reWDEmptyLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDEmptyLink.FindStringSubmatch(s)
		text := strings.TrimSpace(m[1])
		return fmt.Sprintf(`<a href="javascript:;">%s</a>`, html.EscapeString(text))
	})

	// 3a.7 Single-bracket starred link `[*http://... text]`.
	// Same as `[url text]` but the leading `*` is the
	// "open in new tab" marker. Intentionally pushed
	// AFTER every triple-bracket form (3c / 3c.5) so the
	// `[*...Wikidot]` of `[[[*...|Wikidot]]]` doesn't get
	// half-parsed. Single-bracket forms only fire on
	// true single-bracket constructs after the triple-
	// bracket forms have been consumed.
	out = reWDStarredLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDStarredLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			text = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})

	// 3d. Image — generic attribute syntax. Supports
	// `link` (wraps the <img> in an <a>), `width`, `height`,
	// `class`, `style` (arbitrary CSS). Unknown keys are
	// silently dropped.
	out = reWDImage.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDImage.FindStringSubmatch(s)
		src := strings.TrimSpace(m[1])
		attrs := parseImageAttrs(m[2])
		if safe := sanitizeURLForAttr(src); safe != "" {
			return renderImageWrapped(safe, attrs)
		}
		return ""
	})

	// 3d.5 Image alignment prefixes — Wikidot's
	// position-controlled image syntax. Each of the five
	// forms (`[[=image…` / `[[<image…` / `[[>image…` /
	// `[[f<image…` / `[[f>image…`) takes the same URL +
	// attribute tail as the plain `[[image]]` form, but
	// wraps the rendered <img> in a div with an alignment
	// class. The wrapper class is what CSS uses to position
	// the image (center / left / right) or to float it
	// (left-float / right-float, with text wrap).
	out = reWDImgCenter.ReplaceAllStringFunc(out, func(s string) string { return renderAlignedImage(out, s, "center") })
	out = reWDImgLeft.ReplaceAllStringFunc(out, func(s string) string { return renderAlignedImage(out, s, "left") })
	out = reWDImgRight.ReplaceAllStringFunc(out, func(s string) string { return renderAlignedImage(out, s, "right") })
	out = reWDImgFloatL.ReplaceAllStringFunc(out, func(s string) string { return renderAlignedImage(out, s, "floatleft") })
	out = reWDImgFloatR.ReplaceAllStringFunc(out, func(s string) string { return renderAlignedImage(out, s, "floatright") })

	// 3e. `[[button label target]]` — Wikidot-style
	// button link. The label is shown as the button
	// text; the target is the URL the button opens.
	// When the target looks external (http(s)://) we
	// add `rel="nofollow noopener" target="_blank"`
	// — same posture as the `[url text]` external
	// link above. When the target is omitted we
	// default to `#` (Wikidot's spec for a
	// placeholder button).
	out = reWDButton.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDButton.FindStringSubmatch(s)
		label := strings.TrimSpace(m[1])
		target := strings.TrimSpace(m[2])
		if target == "" {
			target = "#"
		}
		safe := sanitizeURLForAttr(target)
		if safe == "" {
			return html.EscapeString(label)
		}
		attrs := ""
		if strings.HasPrefix(safe, "http://") || strings.HasPrefix(safe, "https://") {
			attrs = ` rel="nofollow noopener" target="_blank"`
		}
		return fmt.Sprintf(`<a class="wikidot-button" href="%s"%s>%s</a>`, safe, attrs, html.EscapeString(label))
	})

	// 3f. `[[email]]address[[/email]]` (block form)
	// and `[[email address]]` (single-tag form).
	// Both render to the same `<a class="wikidot-email">`
	// with the address split across `data-user` /
	// `data-domain` so a naive scraper sees no
	// readable email in the HTML source. The
	// on-page link text is the assembled address;
	// browsers show it normally, scrapers don't.
	out = reWDEmailBlock.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDEmailBlock.FindStringSubmatch(s)
		return renderEmailLink(strings.TrimSpace(m[1]))
	})
	out = reWDEmailTag.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDEmailTag.FindStringSubmatch(s)
		return renderEmailLink(strings.TrimSpace(m[1]))
	})

	// ── Phase 4: headings ────────────────────────────────────────────
	// Order longest-prefix first so `++++++` is consumed before `+++++`
	// before `++++` (regex leftmost-longest match would handle this,
	// but explicit ordering keeps the pipeline readable).
	//
	// `+*` variants (skip-toc) are matched FIRST per level so they
	// don't fall through to the plain `+` regex below — both regexes
	// would match the same line otherwise, and the wrong one would
	// win depending on regex order.
	//
	// Each heading also gets a deterministic id ("h2-1", "h3-1", …) so
	// [[toc]] can link to it. Authors who want a stable, named anchor
	// can still use [[a name=…]] explicitly; the auto-ids are
	// reserved for the toc-link target.
	out = reWDHeading.ReplaceAllStringFunc(out, func(s string) string {
		return p.emitHeadingUnified(s)
	})

	// ── Phase 5: horizontal rules ────────────────────────────────────
	// (No-op — moved to Phase 1.5 above so HR runs before
	// strikethrough, which would otherwise eat 3-5 dash runs.)

	// ── Phase 6: line breaks & jump-anchor links & auto-URLs ─────────
	out = reWDLineBreak.ReplaceAllString(out, `<br>`)
	// User mention: `[[user name]]` or `[[*user name]]`
	// (the `*` form is the staff / logged-in variant).
	//
	// When a UserLookup is wired in via RenderContext, we
	// resolve the typed name against the users table; the
	// rendered link then carries:
	//   - `data-username` = the typed name (the author's
	//     intent, preserved for debugging / accessibility)
	//   - `data-user-id`  = the resolved user's ID (or omitted
	//     if the lookup failed)
	//   - `data-avatar`   = the avatar URL (or empty)
	//   - the visible link text is the user's nickname when
	//     one is set, falling back to the typed name, falling
	//     back to the canonical username
	//
	// When the lookup fails (no context / no adapter / unknown
	// name) we still emit the link — same shape, just without
	// the avatar / nickname enrichment. The page profile route
	// (`/user/<slug>`) returns 404 in that case but the
	// hyperlink is still clickable; the next round can wire
	// up a "user not found" placeholder page if the author
	// wants it.
	out = reWDUser.ReplaceAllStringFunc(out, func(s string) string {
		// Recover the leading `*` (if any) and the
		// captured username. The regex is case-
		// insensitive so we lowercase the markup
		// prefix for the comparison.
		lower := strings.ToLower(s)
		staff := strings.HasPrefix(lower, "[[*user ")
		m := reWDUser.FindStringSubmatch(s)
		name := strings.TrimSpace(m[1])
		if name == "" {
			// Author wrote `[[user]]` or
			// `[[*user]]` without a name —
			// render as raw so the author can
			// see the typo.
			return html.EscapeString(s)
		}
		cls := "user-mention"
		if staff {
			cls = "user-mention user-mention-staff"
		}
		// Visible text starts as the typed name. The
		// UserLookup, when present, replaces it with
		// the user's nickname (when set) so the reader
		// sees the display name rather than the login.
		visible := name
		var profile *UserProfile
		if p.ctx != nil && p.ctx.UserLookup != nil {
			profile = p.ctx.UserLookup.UserByName(name)
		}
		// Build the data-* attributes. We always
		// emit `data-username` (the typed name) so
		// the front-end can still show "you typed
		// 'Foo', we found 'foo@example.com'" if
		// needed; `data-user-id` / `data-avatar`
		// are only emitted when the lookup hit.
		attrs := []string{
			`data-username="` + html.EscapeString(name) + `"`,
		}
		if profile != nil {
			if profile.ID != 0 {
				attrs = append(attrs,
					fmt.Sprintf(`data-user-id="%d"`, profile.ID))
			}
			if profile.AvatarURL != "" {
				attrs = append(attrs,
					`data-avatar="`+html.EscapeString(profile.AvatarURL)+`"`)
			}
			if profile.Nickname != "" {
				visible = profile.Nickname
			}
			// Staff override: if the resolved user
			// is staff OR the author used the `*`
			// form, keep the staff class. The `*`
			// form is the historical "logged-in"
			// marker; staff status is now an
			// attribute of the user record, not the
			// markup, but we honour both for
			// backward-compat.
			if profile.IsStaff {
				cls = "user-mention user-mention-staff"
			}
		}
		return fmt.Sprintf(
			`<a class="%s" href="/user/%s" %s>@%s</a>`,
			cls,
			html.EscapeString(slugifyUsername(name)),
			strings.Join(attrs, " "),
			html.EscapeString(visible),
		)
	})
	// Auto-link bare URLs (Wikidot's default behaviour). Runs after the
	// explicit `[url text]` form so a hand-formatted link isn't double-
	// wrapped. The regex excludes `<`, `>`, `[`, `]` to avoid eating
	// into adjacent HTML or wikidot link syntax; trailing punctuation
	// like `,` or `.` may end up inside the href — that's harmless on
	// the link's click target and gets cleaned by the DOMPurify pass
	// on the client.
	//
	// Bare starred URL `*http://...` is handled as a STRIP step only:
	// we remove the `*` prefix here, then let the unprefixed auto-link
	// regex (reWDAutoURL, run immediately below) wrap the URL with the
	// standard new-tab attributes. This avoids double-wrapping the
	// same URL (once by the starred pass, once by the auto-link pass).
	out = reWDBareStarredURL.ReplaceAllStringFunc(out, func(s string) string {
		// Capture group 1 is the full `*URL`. We drop the
		// `*` prefix and let reWDAutoURL add the link
		// wrapper on the next pass. If sanitize rejects
		// the URL we'd lose the `*` with no replacement,
		// but auto-link's own sanitise would also reject,
		// so the net effect is the same: the URL stays
		// raw.
		m := reWDBareStarredURL.FindStringSubmatch(s)
		url := strings.TrimPrefix(m[1], "*")
		if safe := sanitizeURLForAttr(url); safe != "" {
			// Re-emit URL without the `*`. The subsequent
			// reWDAutoURL pass picks up the bare URL and
			// produces the final `<a target=_blank>`.
			return safe
		}
		return s
	})
	out = reWDAutoURL.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAutoURL.FindStringSubmatch(s)
		url := m[1]
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, safe)
		}
		return s
	})
	out = reWDAnchor.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAnchor.FindStringSubmatch(s)
		name := strings.TrimSpace(m[1])
		text := strings.TrimSpace(m[2])
		if text == "" {
			// `[#name]` with no text — emit an empty anchor span so
			// it's a valid drop-in target for any cross-reference.
			return fmt.Sprintf(`<span id="%s" class="wiki-anchor"></span>`, html.EscapeString(name))
		}
		return fmt.Sprintf(`<a href="#%s" class="wiki-anchor-link">%s</a>`, html.EscapeString(name), html.EscapeString(text))
	})

	// ── Phase 7: admonitions ─────────────────────────────────────────
	out = reWDAdmonition.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDAdmonition.FindStringSubmatch(s)
		typ := m[1]
		content := strings.TrimSpace(p.convertNoFootnote(m[2]))
		title, ok := admonitionTitles[typ]
		if !ok {
			title = typ
		}
		return fmt.Sprintf(`<div class="admonition %s"><p class="admonition-title">%s</p>%s</div>`, typ, title, content)
	})

	// ── Phase 8: blockquotes ─────────────────────────────────────────
	out = renderWikidotBlockquotes(out)

	// ── Phase 9: lists (rewritten for nesting) ───────────────────────
	out = renderWikidotLists(out)

	// ── Phase 10: restore stored blocks ──────────────────────────────
	//
	// Substituting only the live `out` string isn't enough: heading
	// text captured in Phase 4 (`p.headings[].Text`) is a snapshot
	// from BEFORE Phase 1's stash passes, so it still contains
	// `%BLOCK_N%` placeholders for any `[[# name]]` (compact anchor)
	// or `@@…@@` (literal) construct that lived inside the heading
	// line. The TOC builder (Phase 12) re-reads `h.Text`, and a
	// place-holdered TOC entry leaks straight through to the
	// browser. Apply the same substitution to the captured headings
	// here so the TOC sees the post-Phase-10 text.
	//
	// Multi-pass because Go map iteration order is random and a
	// block's stored HTML can contain placeholders for other blocks
	// (e.g. `{{@@…@@}}` in a table cell → outer table block stashes
	// the row, inner `@@` block stashes the literal — replacing
	// the table block first leaves the inner `@@` placeholder
	// stranded inside the now-restored table HTML). Loop until a
	// full pass over the map makes no more substitutions.
	for {
		changed := false
		for key, blk := range p.blocks {
			if strings.Contains(out, key) {
				out = strings.ReplaceAll(out, key, blk)
				changed = true
			}
			for i := range p.headings {
				if strings.Contains(p.headings[i].Text, key) {
					p.headings[i].Text = strings.ReplaceAll(p.headings[i].Text, key, blk)
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	// ── Phase 11: paragraph wrapping ─────────────────────────────────
	out = wrapWikidotParagraphs(out)

	// ── Phase 12: TOC resolution ─────────────────────────────────────
	out = p.renderTOC(out)

	// ── Phase 13: footnote list append ───────────────────────────────
	if appendFootnotes {
		out = p.renderFootnoteList(out)
	}

	return out
}

// ── Helper renderers ────────────────────────────────────────────────────

func renderCodeBlock(code, lang string) string {
	c := html.EscapeString(code)
	cls := ""
	if lang != "" {
		cls = fmt.Sprintf(` class="language-%s"`, lang)
	}
	return fmt.Sprintf(`<pre><code%s>%s</code></pre>`, cls, c)
}

// renderEmailLink turns a single address (no brackets, no
// surrounding markup) into the obfuscated `<a class="wikidot-email">`
// markup. The address is split at the LAST `@` into `user` /
// `domain` halves and stored in `data-` attributes; the visible
// link text is the assembled address so the user sees a normal
// mailto link, but a naive HTML scraper looking for the `@` will
// only find it in the rendered text (and modern scrapers already
// extract from there — this is a low-grade obfuscation, not a
// serious anti-harvest measure). When the address is malformed
// (no `@`) we fall back to plain text.
func renderEmailLink(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 0 || at >= len(addr)-1 {
		return html.EscapeString(addr)
	}
	user := addr[:at]
	domain := addr[at+1:]
	display := user + "@" + domain
	return fmt.Sprintf(
		`<a class="wikidot-email" href="mailto:%s" data-user="%s" data-domain="%s">%s</a>`,
		html.EscapeString(display),
		html.EscapeString(user),
		html.EscapeString(domain),
		html.EscapeString(display),
	)
}

// emitHeadingUnified is the single-regex successor to the
// level-by-level `emitHeading` / `emitHeadingStar` pair. The
// heading regex `reWDHeading` captures the plus-prefix as a
// single token (1-6 `+`s, optionally followed by `*` for
// SkipTOC), so the level is just `len(prefix)` and SkipTOC is
// `prefix[len-1] == '*'`. Walking the source in a single
// regex pass (rather than one pass per level) preserves the
// source-order of headings in `p.headings`, which is what the
// TOC builder and the `[[[page#h2-N]]]` / `[#name text]`
// cross-reference helpers both assume.
func (p *wikidotParser) emitHeadingUnified(match string) string {
	m := reWDHeading.FindStringSubmatch(match)
	if m == nil {
		return match
	}
	pluses := m[1]
	star := m[2] == "*"
	level := len(pluses)
	if level < 1 || level > 6 {
		// Defensive — the regex anchor guarantees the
		// length is 1-6, but if someone refactors that
		// and forgets we don't want to misformat.
		return match
	}
	text := strings.TrimSpace(m[3])
	p.headingSeq++
	id := fmt.Sprintf("h%d-%d", level, p.headingSeq)
	if p.headings == nil {
		p.headings = make([]headingEntry, 0, 16)
	}
	p.headings = append(p.headings, headingEntry{
		Level:   level,
		ID:      id,
		Text:    text,
		SkipTOC: star,
	})
	return fmt.Sprintf(`<h%d id="%s">%s</h%d>`, level, id, text, level)
}

// emitHeading converts a heading regex match into the corresponding
// <hN id="...">HTML</hN> tag, records the (level, id, text) tuple
// for the [[toc]] phase, and increments the per-render heading
// sequence counter. skipTOC marks the heading for exclusion from
// the TOC (used by the `+*` heading-prefix form, which still
// gets a stable anchor id for cross-references but doesn't show
// up in the rendered contents list).
func (p *wikidotParser) emitHeading(level int, match string, skipTOC bool) string {
	re := headingRegexFor(level)
	m := re.FindStringSubmatch(match)
	if m == nil {
		return match
	}
	p.headingSeq++
	id := fmt.Sprintf("h%d-%d", level, p.headingSeq)
	text := m[1]
	if p.headings == nil {
		p.headings = make([]headingEntry, 0, 16)
	}
	p.headings = append(p.headings, headingEntry{Level: level, ID: id, Text: text, SkipTOC: skipTOC})
	return fmt.Sprintf(`<h%d id="%s">%s</h%d>`, level, id, text, level)
}

// emitHeadingStar handles the `+*` variant — same id
// sequence counter as the normal headings, so existing
// `[[[page#h2-1]]]` / `[#name text]` cross-references
// keep working. SkipTOC is set so the TOC builder
// omits the entry.
func (p *wikidotParser) emitHeadingStar(level int, match string) string {
	re := headingStarRegexFor(level)
	m := re.FindStringSubmatch(match)
	if m == nil {
		return match
	}
	p.headingSeq++
	id := fmt.Sprintf("h%d-%d", level, p.headingSeq)
	text := m[1]
	if p.headings == nil {
		p.headings = make([]headingEntry, 0, 16)
	}
	p.headings = append(p.headings, headingEntry{Level: level, ID: id, Text: text, SkipTOC: true})
	return fmt.Sprintf(`<h%d id="%s">%s</h%d>`, level, id, text, level)
}

func headingRegexFor(level int) *regexp.Regexp {
	switch level {
	case 1:
		return reWDH1_
	case 2:
		return reWDH2_
	case 3:
		return reWDH3_
	case 4:
		return reWDH4_
	case 5:
		return reWDH5_
	case 6:
		return reWDH6_
	}
	return nil
}

func headingStarRegexFor(level int) *regexp.Regexp {
	switch level {
	case 1:
		return reWDH1Star
	case 2:
		return reWDH2Star
	case 3:
		return reWDH3Star
	case 4:
		return reWDH4Star
	case 5:
		return reWDH5Star
	case 6:
		return reWDH6Star
	}
	return nil
}

// collectFootnoteDefs pre-scans the source for `^[ \t]*[N] text` lines
// (footnote DEFINITIONS, not body references) and stashes them so the
// inline pass doesn't re-process them as body references. The original
// line is replaced with a placeholder, which the paragraph-wrapper
// recognises as a block boundary and emits as an empty <p>. After
// Phase 13's footnote-list append, the visible effect is: definitions
// disappear from the body and re-appear as a numbered <ol> at the
// bottom.
func (p *wikidotParser) collectFootnoteDefs(source string) string {
	return reWDFootnoteDef.ReplaceAllStringFunc(source, func(s string) string {
		m := reWDFootnoteDef.FindStringSubmatch(s)
		n := m[1]
		text := strings.TrimSpace(m[2])
		if p.footnoteDefs == nil {
			p.footnoteDefs = make(map[string]string)
		}
		// First occurrence wins; a duplicate [N] in the source is
		// almost always a paste error and Wikidot's real behaviour
		// is "use the first one", so we mirror that.
		if _, exists := p.footnoteDefs[n]; !exists {
			p.footnoteDefs[n] = text
		}
		// Stash as a block placeholder so the line gets dropped
		// (the wrap-phase treats `%%BLOCK_N%%` as a block boundary
		// and emits an empty <p>; Phase 10 then expands the
		// placeholder into empty content, leaving the line
		// effectively gone).
		return p.storeBlock("")
	})
}

// replaceVars substitutes `%%name%%` from p.vars. Unknown names are
// left as-is so authors can spot typos (matching Wikidot's
// behaviour). Built-in `nil` vars map is a no-op.
func (p *wikidotParser) replaceVars(source string) string {
	if len(p.vars) == 0 {
		return source
	}
	return reWDVar.ReplaceAllStringFunc(source, func(s string) string {
		m := reWDVar.FindStringSubmatch(s)
		if v, ok := p.vars[m[1]]; ok {
			return html.EscapeString(v)
		}
		return s
	})
}

// lookupInclude resolves a `[[include SLUG]]` target via the
// configured PageLookup. Returns nil for a missing lookup / unknown
// slug (caller falls back to raw source). The "default atype"
// fallback uses the current article's type — wikidot articles can
// include wikidot pages, md articles can include md pages, and so
// on.
func (p *wikidotParser) lookupInclude(slug string, attrs map[string]string) *IncludedPage {
	if p.ctx == nil || p.ctx.PageLookup == nil {
		return nil
	}
	atype := p.ctx.ArticleType
	if atype == "" {
		atype = "wikidot"
	}
	return p.ctx.PageLookup.IncludeBySlug(atype, slug)
}

// renderInclude recursively renders an included page's source, with
// the include attributes exposed as %%var%% substitutions. The
// recursive call uses RenderWikidotCtx / RenderMarkdown as
// appropriate so the included content is fully expanded (links,
// tables, code blocks, etc.).
func (p *wikidotParser) renderInclude(target *IncludedPage, attrs map[string]string) string {
	// Compose a vars map: parent vars + include attrs (attrs win).
	vars := make(map[string]string, len(p.vars)+len(attrs))
	for k, v := range p.vars {
		vars[k] = v
	}
	for k, v := range attrs {
		vars[k] = v
	}
	ctx := &RenderContext{
		PageLookup:  nil, // include'd pages don't recursively include by default
		Vars:        vars,
		ArticleType: target.Type,
	}
	switch target.Type {
	case "wikidot":
		return RenderWikidotCtx(ctx, target.Content)
	case "md", "markdown":
		// Markdown doesn't honour %%var%% — pass-through keeps
		// the surface small. If we ever need md-article vars
		// injection, the right place is a pre-pass in this
		// branch (mirror RenderWikidotCtx's vars substitution
		// before the goldmark conversion).
		return RenderMarkdown(target.Content)
	case "bbcode":
		return RenderBBCode(target.Content)
	case "html":
		return target.Content // trusted
	}
	return target.Content
}

// renderModules does paired `[[module Name attrs]]…[[/module]]`
// matching, evaluates each supported module, and replaces the
// whole construct with the rendered HTML. Inner templates can
// contain their own wikidot syntax (lists, links, %%vars%%), so
// the module's output is run through inlineOnly / the full
// pipeline as appropriate.
func (p *wikidotParser) renderModules(source string) string {
	var sb strings.Builder
	last := 0
	opens := reWDModuleOpen.FindAllStringSubmatchIndex(source, -1)
	if len(opens) == 0 {
		return source
	}
	// Pair each [[module …]] with the next [[/module]] AFTER it.
	// We don't try to handle nested modules (Wikidot's own
	// semantics disallow it), so a simple linear scan works.
	for _, loc := range opens {
		openStart, openEnd := loc[0], loc[1]
		// Append everything before this open tag verbatim.
		if openStart > last {
			sb.WriteString(source[last:openStart])
		}
		name := source[loc[2]:loc[3]]
		rawAttrs := source[loc[4]:loc[5]]
		attrs := parseModuleAttrs(rawAttrs)
		// Find the next [[/module]] after openEnd.
		closeStart := reWDModuleClose.FindStringIndex(source[openEnd:])
		if closeStart == nil {
			// Unclosed module — keep everything verbatim so the
			// author can see the broken markup.
			sb.WriteString(source[openStart:openEnd])
			last = openEnd
			continue
		}
		bodyStart := openEnd
		bodyEnd := openEnd + closeStart[0]
		afterEnd := openEnd + closeStart[1]
		body := source[bodyStart:bodyEnd]

		rendered := p.runModule(name, attrs, body)
		// Module output is a complete HTML fragment (e.g.
		// `<ul class="wikidot-module-list">…</ul>`). If we
		// inline it directly, the downstream list pass
		// (Phase 9) would see any literal `* ` characters
		// in the row template and try to convert them to
		// list items — for a `[[module ListPages]]` body
		// like `* %%title%%`, that produces a spurious
		// nested <ul> under each <li>. Stash the rendered
		// output as a block and let Phase 10 restore it
		// AFTER the list pass, so the literal `* ` chars
		// are protected by the placeholder syntax.
		sb.WriteString(p.storeBlock(rendered))
		last = afterEnd
	}
	if last < len(source) {
		sb.WriteString(source[last:])
	}
	return sb.String()
}

// runModule dispatches a single module call to the per-module
// handler. The body is the raw inner template; the per-module
// helper takes care of substituting %%vars%% and wrapping each
// result in the appropriate list/anchor markup.
func (p *wikidotParser) runModule(name string, attrs map[string]string, body string) string {
	if p.ctx == nil || p.ctx.PageLookup == nil {
		return fmt.Sprintf(`<p class="wikidot-module-error">[[module %s]] 需要后端支持 (PageLookup 未配置)</p>`, html.EscapeString(name))
	}
	switch strings.ToLower(name) {
	case "listpages":
		return p.runListPages(attrs, body)
	case "randompage":
		return p.runRandomPage(attrs, body)
	default:
		return fmt.Sprintf(`<p class="wikidot-module-error">[[module %s]] 不支持 (仅支持 ListPages / RandomPage)</p>`, html.EscapeString(name))
	}
}

// runListPages — `[[module ListPages category="X" limit="5" order="created_at desc"]]body[[/module]]`.
// The body is a template evaluated per row. `%%title%%`, `%%slug%%`,
// `%%author_name%%`, `%%created_at%%`, `%%tags%%`, `%%rating%%` are
// substituted. We don't try to parse `* body` as Wikidot list
// syntax — instead the body is run through inlineOnly (so bold,
// links, %%var%%, etc. all work) and wrapped in <li>. The whole
// thing is wrapped in <ul class="wikidot-module-list">.
func (p *wikidotParser) runListPages(attrs map[string]string, body string) string {
	category := attrs["category"]
	if category == "" {
		category = "*"
	}
	limit := 10
	if v, ok := attrs["limit"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	order := attrs["order"]
	entries := p.ctx.PageLookup.ListPages(category, limit, order)
	if len(entries) == 0 {
		return `<ul class="wikidot-module-list wiki-module-empty"><li><em>没有匹配的文章。</em></li></ul>`
	}
	// Strip a leading " * " if the template is a single bullet
	// line — Wikidot authors expect this to expand as a list, and
	// we wrap each entry in <li> so the `*` would be a stray
	// marker.
	tpl := body
	tpl = strings.TrimLeft(tpl, " \t")
	tpl = strings.TrimPrefix(tpl, "* ")
	tpl = strings.TrimPrefix(tpl, "*\t")

	var sb strings.Builder
	sb.WriteString(`<ul class="wikidot-module-list">`)
	// Trim leading/trailing whitespace (incl. newlines) from
	// the body before the prefix-strip — the regex pair
	// between the open tag and `[[/module]]` typically
	// captures the surrounding newlines as part of the
	// body, and a stray `\n* %%title%%` template would
	// otherwise leak a literal `* ` into the output.
	tpl = strings.TrimSpace(tpl)
	tpl = strings.TrimPrefix(tpl, "* ")
	tpl = strings.TrimPrefix(tpl, "*\t")

	for _, e := range entries {
		row := tpl
		row = strings.ReplaceAll(row, "%%title%%", e.Title)
		row = strings.ReplaceAll(row, "%%slug%%", e.Slug)
		row = strings.ReplaceAll(row, "%%author_name%%", e.AuthorName)
		row = strings.ReplaceAll(row, "%%author_nickname%%", e.AuthorNickname)
		if !e.CreatedAt.IsZero() {
			row = strings.ReplaceAll(row, "%%created_at%%", e.CreatedAt.Format("2006-01-02"))
		}
		row = strings.ReplaceAll(row, "%%tags%%", strings.Join(e.Tags, ", "))
		row = strings.ReplaceAll(row, "%%rating%%", fmt.Sprintf("%.1f", e.Rating))
		sb.WriteString("<li>")
		sb.WriteString(inlineOnly(row))
		sb.WriteString("</li>")
	}
	sb.WriteString("</ul>")
	return sb.String()
}

// runRandomPage — `[[module RandomPage category="X"]]label[[/module]]`.
// Emits a single anchor pointing to one random article in the
// category. If the body is empty, the article title is used as
// link text.
func (p *wikidotParser) runRandomPage(attrs map[string]string, body string) string {
	category := attrs["category"]
	if category == "" {
		category = "*"
	}
	entry := p.ctx.PageLookup.RandomPage(category)
	if entry == nil {
		return `<a class="wikidot-random-page wiki-module-empty" href="#">没有可选页面</a>`
	}
	text := strings.TrimSpace(body)
	if text == "" {
		text = entry.Title
	}
	href := "/wikidot/" + entry.Slug
	return fmt.Sprintf(`<a class="wikidot-random-page" href="%s">%s</a>`, html.EscapeString(href), html.EscapeString(text))
}

// parseModuleAttrs parses the raw attribute tail of `[[module Name …]]`
// into a map. Both `key="val"` (quoted) and `key=val` (bare) are
// accepted; spaces or pipes separate the pairs.
func parseModuleAttrs(raw string) map[string]string {
	out := make(map[string]string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	// Strip a leading `|` so `category="*"|limit=5` parses the
	// same as `category="*" limit=5`.
	raw = strings.TrimLeft(raw, "|")
	// Tokenize on `|` (Wikidot's official separator) OR on
	// whitespace — both are seen in the wild.
	tokens := splitModuleTokens(raw)
	for _, t := range tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		eq := strings.Index(t, "=")
		if eq < 0 {
			out[strings.ToLower(t)] = ""
			continue
		}
		k := strings.ToLower(strings.TrimSpace(t[:eq]))
		v := strings.TrimSpace(t[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}

func splitModuleTokens(s string) []string {
	// Walk character by character to keep `key="value with space"`
	// intact. The `|`-separator is only a token boundary when it's
	// NOT inside quotes.
	var (
		tokens []string
		buf    strings.Builder
		inQ    bool
		qCh    byte
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQ:
			buf.WriteByte(c)
			if c == qCh {
				inQ = false
			}
		case c == '"' || c == '\'':
			inQ = true
			qCh = c
			buf.WriteByte(c)
		case c == '|':
			tokens = append(tokens, buf.String())
			buf.Reset()
		case c == ' ' || c == '\t':
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteByte(c)
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// parseIncludeAttrs is the same idea as parseModuleAttrs but for
// `[[include slug | k=v | k="v"]]`. The slug itself is the first
// token in the include regex; the attrs start at the `|` separator.
func parseIncludeAttrs(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "|") {
		return map[string]string{}
	}
	return parseModuleAttrs(raw[1:])
}

// renderTOC replaces `[[__TOC__]]` / `[[__FTOC_LEFT__]]` /
// `[[__FTOC_RIGHT__]]` placeholders (stored in Phase 1p) with
// a nested <ul> built from the headings collected during
// Phase 4. If no headings were collected, the toc placeholder
// is left as-is so the author can see why it's empty.
//
// `[[f<toc]]` / `[[f>toc]]` produce a floated version of the
// toc — the outer div has an extra `float:left` /
// `float:right` class so the surrounding text wraps around
// the contents list.
//
// Structure: a real `<ul>`-in-`<ul>` nesting reflects heading
// hierarchy (h2 outer, h3 nested inside its parent h2, …).
// The previous implementation was a flat `<ul>` that
// delegated indent to a CSS `.toc-h3 { padding-left: … }`
// rule; nested HTML is more readable in devtools, copies
// cleanly when users grab the markup for their own pages,
// and survives user-style-stripping (some browsers / reader
// modes drop CSS but keep element semantics). The CSS rule
// was kept as a fallback for the `[[toc]]` no-block case so
// the visual layout matches the old behaviour if a stylesheet
// override is active.
//
// Output shape (sample):
//
//	<div class="wikidot-toc" role="navigation" aria-label="Table of contents">
//	  <div class="wikidot-toc-title">Table of Contents</div>
//	  <ul class="wikidot-toc-list">
//	    <li class="toc-li toc-h2">
//	      <a href="#h2-1" class="toc-link toc-link-h2"><span class="toc-text">H2-a</span></a>
//	      <ul>
//	        <li class="toc-li toc-h3">
//	          <a href="#h3-1" class="toc-link toc-link-h3"><span class="toc-text">H3-a</span></a>
//	        </li>
//	      </ul>
//	    </li>
//	  </ul>
//	</div>
func (p *wikidotParser) renderTOC(source string) string {
	if !strings.Contains(source, "[[__TOC__]]") &&
		!strings.Contains(source, "[[__FTOC_LEFT__]]") &&
		!strings.Contains(source, "[[__FTOC_RIGHT__]]") {
		return source
	}
	// Build the inner HTML once and substitute into each
	// of the three possible placeholders so a single
	// article can have any combination of them.
	var inner string
	if len(p.headings) == 0 {
		inner = `<p class="wikidot-toc-empty"><em>本文暂无章节</em></p>`
	} else {
		inner = renderWikidotTOCList(p.headings, 2 /* minTocLevel */)
	}
	// Wrap with the title + accessibility attributes.
	// We emit the title even when empty (in the no-heading
	// case the body's own "本文暂无章节" message serves the
	// role); CSS hides the title if the toc is collapsed.
	wrapped := `<div class="wikidot-toc" role="navigation" aria-label="Table of contents">` +
		`<div class="wikidot-toc-title">Table of Contents</div>` +
		inner +
		`</div>`
	source = strings.ReplaceAll(source, "[[__TOC__]]", wrapped)
	source = strings.ReplaceAll(source, "[[__FTOC_LEFT__]]",
		strings.Replace(wrapped, `class="wikidot-toc"`, `class="wikidot-toc wikidot-toc-float-left"`, 1))
	source = strings.ReplaceAll(source, "[[__FTOC_RIGHT__]]",
		strings.Replace(wrapped, `class="wikidot-toc"`, `class="wikidot-toc wikidot-toc-float-right"`, 1))
	return source
}

// renderWikidotTOCList emits a nested `<ul>` reflecting the
// heading-level hierarchy from `headings`. `minTocLevel` is the
// lowest level included — Wikidot's default is H2 because H1 is
// treated as the article title (h1 entries still get anchor
// IDs and live in `headings`, they're just absent from the toc).
//
// The walker builds a parent→children tree first, then
// serialises it. Building the tree is more readable than
// tracking a depth counter inline because skipping a level
// (e.g. `++ H2, ++++ H4`) just falls through to the next
// ancestor on the stack without breaking the previous <ul>.
//
// Each <li> entry carries:
//   - `class="toc-li toc-hN"` so CSS can style levels
//     independently (font-size, indent, marker colour);
//   - an `<a class="toc-link toc-link-hN">` so styling that
//     targets the link alone (visited state, hover halo)
//     has a stable hook;
//   - `<span class="toc-text">…</span>` so the link text
//     can be styled / measured / replaced by JS without
//     touching the `<a>` element itself.
//
// `+*` headings (SkipTOC) are silently dropped; their
// anchors still resolve for `[#name text]` cross-references.
func renderWikidotTOCList(headings []headingEntry, minTocLevel int) string {
	tree := buildWikidotTOCTree(headings, minTocLevel)
	var sb strings.Builder
	sb.WriteString(`<ul class="wikidot-toc-list">`)
	sb.WriteString(renderTOCChildren(tree))
	sb.WriteString(`</ul>`)
	return sb.String()
}

// tocNode is the in-memory TOC tree. A nil slice of children
// means a leaf; rendering for a leaf emits a self-contained
// `<li>` without a nested `<ul>`.
type tocNode struct {
	heading  headingEntry
	children []*tocNode
}

// buildWikidotTOCTree walks `headings` in source order and
// attaches each non-skipped heading as a child of the most
// recent ancestor at a SHALLOWER level. The walker maintains
// a small stack of the currently-open path: when a heading
// arrives at level L, the stack is popped until its top has
// level < L (so the new entry's parent is the previous entry
// at a strictly shallower level — never a sibling), and the
// new node becomes a child of the popped-to ancestor.
func buildWikidotTOCTree(headings []headingEntry, minTocLevel int) *tocNode {
	if minTocLevel < 1 {
		minTocLevel = 1
	}
	root := &tocNode{} // dummy; children are real toc entries.
	stack := []*tocNode{root}
	for _, h := range headings {
		if h.SkipTOC {
			continue
		}
		if h.Level < minTocLevel || h.Level > 6 {
			continue
		}
		// Pop until top has level < h.Level (strictly
		// shallower). Edge case: an H4 following an H2
		// (without an H3 between) just attaches to the
		// H2's children — no synthetic intermediate level.
		for len(stack) > 1 && stack[len(stack)-1].heading.Level >= h.Level {
			stack = stack[:len(stack)-1]
		}
		node := &tocNode{heading: h}
		top := stack[len(stack)-1]
		top.children = append(top.children, node)
		stack = append(stack, node)
	}
	return root
}

// renderTOCChildren serialises the children of `node` into a
// nested `<ul>`. The caller is responsible for wrapping this
// in the outer `<ul class="wikidot-toc-list">` (or for a
// recursion-level inner `<ul>`).
func renderTOCChildren(node *tocNode) string {
	var sb strings.Builder
	for _, child := range node.children {
		h := child.heading
		sb.WriteString(`<li class="toc-li toc-h`)
		sb.WriteString(strconv.Itoa(h.Level))
		sb.WriteString(`">`)
		sb.WriteString(fmt.Sprintf(
			`<a href="#%s" class="toc-link toc-link-h%d"><span class="toc-text">%s</span></a>`,
			h.ID, h.Level, html.EscapeString(h.Text)))
		if len(child.children) > 0 {
			sb.WriteString(`<ul>`)
			sb.WriteString(renderTOCChildren(child))
			sb.WriteString(`</ul>`)
		}
		sb.WriteString(`</li>`)
	}
	return sb.String()
}

// renderFootnoteList appends a `<ol class="footnotes">` block at
// the end of the document IF any footnote definitions were
// collected in Phase 0. The list items link back to the body
// references (`<a href="#fnref-N">↩</a>`). Order: numeric
// ascending. A `[[footnoteblock]]` marker in the source
// suppresses this append and records an optional
// `title="..."` override for the section label.
func (p *wikidotParser) renderFootnoteList(source string) string {
	if len(p.footnoteDefs) == 0 || p.footnoteSuppressed {
		return source
	}
	return source + renderFootnoteSection(p.footnoteDefs, p.footnoteTitle)
}

// renderFootnoteSection builds the `<section class="footnotes">…</section>`
// markup from a definitions map. Shared by renderFootnoteList
// (auto-append path) and the `[[footnoteblock]]` in-place
// replacement. Returns "" when the map is empty so callers
// can drop the marker without leaving an empty `<section>`.
func renderFootnoteSection(defs map[string]string, title string) string {
	if len(defs) == 0 {
		return ""
	}
	keys := make([]int, 0, len(defs))
	for k := range defs {
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		keys = append(keys, n)
	}
	sortInts(keys)
	label := title
	if label == "" {
		label = "脚注"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<section class="footnotes"><h2 class="footnotes-title">%s</h2><ol>`, html.EscapeString(label)))
	for _, n := range keys {
		def := defs[strconv.Itoa(n)]
		sb.WriteString(fmt.Sprintf(`<li id="fn-%d">%s <a class="footnote-backref" href="#fnref-%d" title="回到正文">↩</a></li>`, n, inlineOnly(def), n))
	}
	sb.WriteString(`</ol></section>`)
	return sb.String()
}

// sortInts is a tiny insertion sort — Go's stdlib sort package
// is fine, but pulling it in for a 5-key map is overkill, and
// the wikidot parser package already has no other use of sort.
func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j] > v {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// ── Table rendering ─────────────────────────────────────────────────────

// renderWikidotTableRowLine parses one `||…||` line into cells. A cell
// starting with `~` is a header cell. Empty cells (from `|| ||` or
// `||||` merges) are collapsed to a single cell with `colspan`
// proportional to the number of `||` separators consumed.
//
// Inline-only on cell content so <p> doesn't end up inside <td>/<th>.
// Authors wanting block content inside a cell should use the
// [[table]]…[[/table]] block syntax with HTML authored directly.
func renderWikidotTableRowLine(p *wikidotParser, line string, headerRow bool) string {
	line = strings.TrimSpace(line)
	// Strip leading/trailing `||`
	line = strings.TrimPrefix(line, "||")
	line = strings.TrimSuffix(line, "||")
	// Split on `||` and walk — consecutive empty splits indicate
	// a `||||` cell-separator, which the original parser rendered
	// as a single empty <th>/<td>. We keep that behaviour and
	// additionally emit `colspan="N"` so the table actually
	// collapses instead of leaving a visible empty column.
	raw := strings.Split(line, "||")
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		// Count runs of empty cells ahead.
		colspan := 1
		for i+1 < len(raw) && strings.TrimSpace(raw[i+1]) == "" {
			colspan++
			i++
		}
		c := strings.TrimSpace(raw[i])
		isHeader := headerRow || strings.HasPrefix(c, "~")
		tag := "td"
		if isHeader {
			tag = "th"
			if c != "" {
				c = strings.TrimPrefix(c, "~")
				c = strings.TrimSpace(c)
			}
		}
		if colspan > 1 {
			sb.WriteString(fmt.Sprintf(`<%s colspan="%d">%s</%s>`, tag, colspan, inlineOnly(c), tag))
		} else {
			sb.WriteString(fmt.Sprintf(`<%s>%s</%s>`, tag, inlineOnly(c), tag))
		}
	}
	return sb.String()
}

// joinMultiLineTableRows is the pre-pass for table-row
// rendering. It walks the source line-by-line and, for each
// `||…` line that does NOT end with `||` on the same line
// (i.e. a multi-line row opener), collects subsequent lines
// until the matching line that DOES end with `||`. The
// joined cell content replaces each internal `\n` with a
// single space so the multi-line cell becomes one
// whitespace-separated cell.
//
// Wikidot's spec example:
//
//   |||||| 超长 _
//   内容 8||
//
// → after joinMultiLineTableRows:
//
//   |||||| 超长   内容 8||
//
// (the ` _\n` continuation marker is consumed; the
// newline becomes a single space so the cell still reads
// naturally without a hard break).
//
// Unmatched openers (no later line ending with `||`) are
// left verbatim so the author can see the typo. The
// function never produces input that would break a
// well-formed row, so the downstream renderWikidotTableRows
// pass can rely on each row fitting a single source line.
//
// Limitations:
//   - Only `||…` lines (not `[[table]]…[[/table]]` blocks)
//     are processed here — the [[table]] form already
//     accepts newlines inside cells.
//   - Inside a multi-line row the literal ` _\n` is the
//     continuation marker; without the marker (e.g. a bare
//     newline) the cell still joins, but a literal `_<space>\n`
//     in author prose outside the row is left alone.
func joinMultiLineTableRows(text string) string {
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		// Multi-line opener: starts with `||` but does NOT
		// end with `||`. A line that ends with `||` is a
		// single-line row and is left untouched (the
		// downstream pass handles it).
		if strings.HasPrefix(trimmed, "||") && !strings.HasSuffix(trimmed, "||") && len(trimmed) >= 2 {
			parts := []string{lines[i]}
			j := i + 1
			joined := false
			for j < len(lines) {
				next := strings.TrimSpace(lines[j])
				if strings.HasPrefix(next, "||") && strings.HasSuffix(next, "||") && len(next) >= 4 {
					parts = append(parts, lines[j])
					joined = true
					break
				}
				// Continuation line. We discard any `_ _`
				// marker at the end (the spec example uses
				// ` _\n` to signal continuation) and just
				// keep the trimmed text. The cells of a
				// joined row are separated by a single
				// space when the final row-line is
				// emitted.
				cleaned := strings.TrimRight(lines[j], " \t")
				cleaned = strings.TrimRight(cleaned, "_")
				cleaned = strings.TrimRight(cleaned, " \t")
				parts = append(parts, cleaned)
				j++
			}
			if joined {
				// Emit the joined line as a single
				// source line. We replace the source
				// newlines with `\n` so the row
				// pipeline sees one logical row;
				// cell-content newlines become literal
				// `\n` in the cell and the rendering
				// pipeline converts them to a space
				// when emitting <td> content.
				sb.WriteString(strings.Join(parts, " "))
			} else {
				// Unmatched opener — emit each line
				// verbatim so the author sees the
				// typo.
				for k, p := range parts {
					sb.WriteString(p)
					if k < len(parts)-1 {
						sb.WriteString("\n")
					}
				}
			}
			if i < len(lines)-1 || joined {
				// Only re-emit a trailing newline if
				// there are more lines after this
				// region. Parts already contain all
				// consumed lines; if `joined`, we've
				// consumed through `j`, which we
				// updated past. Loop's `i = j + 1`
				// below handles the index.
				_ = joined
			}
			if joined && i < len(lines)-1 {
				sb.WriteString("\n")
			}
			i = j + 1
			continue
		}
		sb.WriteString(lines[i])
		if i < len(lines)-1 {
			sb.WriteString("\n")
		}
		i++
	}
	return sb.String()
}

// renderWikidotTableRows finds contiguous runs of `||…||` lines and
// replaces each run with a single `<table>` placeholder. The first row
// is treated as the header row (Wikidot convention; if the first row's
// cells don't have `~` markers, they're still rendered as <th>).
//
// Before renderWikidotTableRows the source is run through
// joinMultiLineTableRows so multi-line cells (Wikidot's `_ _ \n`
// continuation marker inside a `||…||` block) collapse into a single
// row-line here.
func renderWikidotTableRows(p *wikidotParser, text string) string {
	lines := strings.Split(text, "\n")
	var result strings.Builder
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "||") && strings.HasSuffix(t, "||") && len(t) >= 4 {
			// Collect contiguous || lines.
			j := i
			for j < len(lines) {
				t2 := strings.TrimSpace(lines[j])
				if !(strings.HasPrefix(t2, "||") && strings.HasSuffix(t2, "||") && len(t2) >= 4) {
					break
				}
				j++
			}
			// lines[i:j] is a table.
			var sb strings.Builder
			sb.WriteString(`<table class="wiki-table"><tbody>`)
			for k, line := range lines[i:j] {
				// First row is the header unless none of its cells
				// actually use the `~` marker — in that case we still
				// promote them to <th> because Wikidot readers expect
				// the first row to be the header. Authors wanting a
				// body-only table can use [[table]]…[[/table]].
				isHeader := k == 0
				sb.WriteString("<tr>")
				sb.WriteString(renderWikidotTableRowLine(p, line, isHeader))
				sb.WriteString("</tr>")
			}
			sb.WriteString("</tbody></table>")
			result.WriteString(p.storeBlock(sb.String()))
			i = j
		} else {
			result.WriteString(lines[i])
			if i < len(lines)-1 {
				result.WriteString("\n")
			}
			i++
		}
	}
	return result.String()
}

// parseImageAttrs extracts `key="value"` pairs from the
// attribute tail of a `[[image ...]]` block. Returns an
// empty map for an empty tail. Keys are normalised to
// lowercase so callers can look them up case-insensitively.
func parseImageAttrs(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" {
		return out
	}
	for _, m := range reWDImageAttr.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	return out
}

// parseCollapsibleAttrs is the `[[collapsible ...]]`-block
// equivalent of parseImageAttrs. It pulls `show` / `hide` /
// `folded` / `hideLocation` out of the attribute tail; the
// regex is shared with the image parser, so a single
// key="value" pair works either way.
func parseCollapsibleAttrs(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" {
		return out
	}
	for _, m := range reWDCollapsibleAttr.FindAllStringSubmatch(raw, -1) {
		out[strings.ToLower(m[1])] = m[2]
	}
	return out
}

// buildImageTag composes the <img> tag from a sanitised
// source URL and a map of recognised attributes. We always
// emit `max-width:100%` so a wide source image doesn't blow
// out the article column — user-supplied `style` is
// prepended so the author can still override other props
// (the `max-width` is preserved by appending after the
// user's value).
func buildImageTag(src string, attrs map[string]string) string {
	var sb strings.Builder
	sb.WriteString(`<img src="`)
	sb.WriteString(src)
	sb.WriteString(`" alt="" loading="lazy"`)
	if w, ok := attrs["width"]; ok && w != "" {
		sb.WriteString(` width="`)
		sb.WriteString(html.EscapeString(w))
		sb.WriteString(`"`)
	}
	if h, ok := attrs["height"]; ok && h != "" {
		sb.WriteString(` height="`)
		sb.WriteString(html.EscapeString(h))
		sb.WriteString(`"`)
	}
	if cls, ok := attrs["class"]; ok && cls != "" {
		sb.WriteString(` class="`)
		sb.WriteString(sanitizeAnchorID(cls))
		sb.WriteString(`"`)
	}
	userStyle := ""
	if st, ok := attrs["style"]; ok {
		if css := sanitizeCSSValue(st); css != "" {
			userStyle = css
		}
	}
	if userStyle != "" {
		sb.WriteString(` style="`)
		sb.WriteString(userStyle)
		// sanitizeCSSValue accepts declarations with or
		// without a trailing `;` (it normalises by
		// appending one if missing). We add a space
		// before max-width so the two declarations
		// are visually separated regardless of how
		// the author wrote the trailing punctuation.
		sb.WriteString(` max-width:100%"`)
	} else {
		sb.WriteString(` style="max-width:100%"`)
	}
	sb.WriteString(`>`)
	return sb.String()
}

// renderImageWrapped composes the `<img>` tag (via
// buildImageTag) and, if the author supplied a `link`
// attribute, wraps the result in an `<a>` whose form depends
// on the attribute value (see the comments in the link
// handling below). The link semantics mirror what Wikidot
// does for `[[image … link="…"]]`:
//
//   link="*url"        external URL, opens in new tab
//   link="http(s)://"  external URL (no `*` prefix), opens in new tab
//   link="/path"       internal relative path
//   link="#anchor"     in-page anchor link
//   link="wiki-page"   slug → /wikidot/<slug>
//
// Link values that fail `sanitizeURLForAttr` fall through as
// a bare `<img>` so the author can see the typo.
func renderImageWrapped(src string, attrs map[string]string) string {
	img := buildImageTag(src, attrs)
	link, ok := attrs["link"]
	if !ok || link == "" {
		return img
	}
	// `*url` — strip the star and emit with new-tab attributes.
	if strings.HasPrefix(link, "*") {
		target := strings.TrimPrefix(link, "*")
		if safe := sanitizeURLForAttr(target); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, img)
		}
		return img
	}
	// `#anchor` — in-page anchor link. No new-tab.
	if strings.HasPrefix(link, "#") {
		anchor := strings.TrimPrefix(link, "#")
		// Sanitise the anchor id the same way as `[#name]` does:
		// HTML-escape, no further filtering (Wikidot anchor ids
		// accept Chinese / non-ASCII characters).
		if anchor == "" {
			return img
		}
		return fmt.Sprintf(`<a href="#%s">%s</a>`, html.EscapeString(anchor), img)
	}
	// `http://` / `https://` / `/path` (existing
	// sanitizeURLForAttr allow-list covers each case). Anything
	// else (e.g. `mailto:foo`) returns "" and we fall through
	// to a bare <img>. External http(s) URLs do NOT add
	// `rel="nofollow noopener" target="_blank"` here — that
	// is the contract for the `link="*url"` (starred) form
	// only. The bare `link="http://…"` form preserves the
	// historical behaviour (a plain wrap with no extras).
	if safe := sanitizeURLForAttr(link); safe != "" {
		return fmt.Sprintf(`<a href="%s">%s</a>`, safe, img)
	}
	// `wiki-page` (a bare slug, no `/wikidot/` prefix) is treated
	// as an internal wikidot page reference — same posture as the
	// `[1] / some-page` URL routing used by the article-detail
	// page (`/wikidot/<slug>`). The slug must be URL-safe: only
	// letters, digits, dash, underscore, dot, percent, and the
	// category-namespace `:` colon. Any other character (whitespace,
	// `/`, `?`, `#`, etc.) makes the slug ambiguous, so we fall
	// through to a bare `<img>` rather than guessing.
	if link != "" && !strings.ContainsAny(link, " \t\n/?#\"'<>") {
		slugSafe := true
		for _, c := range link {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_' || c == ':' ||
				c == '.' || c == '%') {
				slugSafe = false
				break
			}
		}
		if slugSafe {
			return fmt.Sprintf(`<a href="/wikidot/%s">%s</a>`, link, img)
		}
	}
	return img
}

// renderAlignedImage processes an `[[=image…]]` / `[[<image…]]` /
// `[[>image…]]` / `[[f<image…]]` / `[[f>image…]]` match. The
// regex captures (URL, attr-tail); we route both through the
// existing buildImageTag helper and wrap the result in a div
// carrying the alignment class so the front-end can position
// the image without needing extra CSS per article.
func renderAlignedImage(_ string, raw string, align string) string {
	// Pick the regex matching the alignment that's being
	// rendered. Each `render*` invocation is a closure over
	// one of the five prefix regexes, so we resolve the
	// match by repeating the same regex find here.
	m := reMatchAlign(raw, align)
	if m == nil {
		return raw
	}
	src := strings.TrimSpace(m[1])
	if src == "" {
		return raw
	}
	attrs := parseImageAttrs(m[2])
	if safe := sanitizeURLForAttr(src); safe != "" {
		img := renderImageWrapped(safe, attrs)
		return fmt.Sprintf(`<div class="wikidot-image-wrap wikidot-image-%s">%s</div>`, align, img)
	}
	return raw
}

// reMatchAlign returns the FindStringSubmatch result for the
// alignment-prefixed image whose alignment is `align` (one of
// "center", "left", "right", "floatleft", "floatright"). The
// calling closure in Phase 3d.5 captures the corresponding
// regex without having to pass it through `renderAlignedImage`
// as a parameter.
func reMatchAlign(raw string, align string) []string {
	var re *regexp.Regexp
	switch align {
	case "center":
		re = reWDImgCenter
	case "left":
		re = reWDImgLeft
	case "right":
		re = reWDImgRight
	case "floatleft":
		re = reWDImgFloatL
	case "floatright":
		re = reWDImgFloatR
	default:
		return nil
	}
	return re.FindStringSubmatch(raw)
}

// renderWikidotFormBlocks handles the paired `[[form …]]…[[/form]]`
// block (Phase 1f.6). The block opener is `[[form attrs]]` and
// the close is `[[/form]]`; a depth counter balances nested
// `[[form]]` forms so an inner opener doesn't trip the outer
// closure early.
//
// Inner form widgets (`[[input …]]`, `[[textarea …]]…[[/textarea]]`,
// `[[button …]]`, `[[checkbox …]]`, `[[radio …]]`,
// `[[select …]]…[[/select]]`, `[[option …]]…[[/option]]`)
// are substituted by `substituteFormWidgets` into the
// corresponding HTML form controls. The text OUTSIDE the
// widget tags (the prose, the labels, the hints) is run
// through `inlineOnly` so wikidot inline formatting
// (`**bold****, `//italic//``, `[link]`) survives, but
// block-level constructs inside a form block don't fire
// (a stray `[[div …]]` inside a form would be left raw as
// the author intended; we don't recursively render the
// form body through the full pipeline because the form
// is a strict container).
//
// Implementation: byte-level walk from each opener. At
// each step we look for the literal `[[form` (with a
// non-name-char after to avoid `[[formula]]`) or
// `[[/form]]` and update the depth counter. When the
// depth returns to 0 we have the matching close.
func renderWikidotFormBlocks(source string) string {
	const openTok = "[[form"
	const closeTok = "[[/form]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		// Find next opener. We require `[[form` followed by either
		// whitespace + attrs, OR `]]` directly (the bare-form
		// form), OR by the closing `]]`. The substring match is
		// restricted to the literal `[[form` so `[[formula]]`
		// (false-positive) doesn't trip us.
		openerStart := indexOfFormOpener(source, i)
		if openerStart < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit everything before the opener verbatim.
		sb.WriteString(source[i:openerStart])
		// Find the close `]]` of the opener tag itself.
		closeIdx := strings.Index(source[openerStart:], "]]")
		if closeIdx < 0 {
			// Unbalanced opener with no `]]` — leave verbatim.
			sb.WriteString(source[openerStart:])
			return sb.String()
		}
		openerEnd := openerStart + closeIdx + 2
		openerTag := source[openerStart:openerEnd]
		// Byte-level depth-counted walk for the matching close.
		// Walk from `openerStart` (NOT `openerEnd`) so that the
		// walk encounters the opener we just found. Start
		// `depth` at 0; the opener we re-encounter sets depth=1.
		// A close that brings depth back to 0 is the matching
		// close for the outer opener — anything between the
		// openerEnd and that close is the form body.
		j := openerStart
		depth := 0
		for j < len(source) {
			// Check for next opener at the current position:
			// require `[[form` followed by a non-name char.
			if isAtFormOpener(source, j) {
				// Find this opener's own `]]` to know the full token
				// length.
				cl := strings.Index(source[j:], "]]")
				if cl < 0 {
					// Defensive — opener found but no close.
					sb.WriteString(source[openerStart:])
					return sb.String()
				}
				depth++
				j = j + cl + 2
				continue
			}
			// Check for next close `[[/form]]`.
			if strings.HasPrefix(source[j:], closeTok) {
				depth--
				closeEnd := j + len(closeTok)
				if depth == 0 {
					body := source[openerEnd:j]
					inner := substituteFormWidgets(body)
					sb.WriteString(`<form`)
					// Pull the attribute tail out of the opener tag.
					sb.WriteString(extractFormAttrs(openerTag))
					sb.WriteString(`>`)
					sb.WriteString(inner)
					sb.WriteString(`</form>`)
					i = closeEnd
					goto nextBlock
				}
				j = closeEnd
				continue
			}
			j++
		}
		// EOF without a matching close — leave the opener raw.
		sb.WriteString(source[openerStart:])
		return sb.String()
	nextBlock:
	}
	return sb.String()
}

// isAtFormOpener reports whether `source` at-or-after `i`
// starts with a valid `[[form` opener token (i.e. `[[form`
// followed by whitespace, which means at least one
// attribute is being defined). Bare `[[form]]` (no
// attributes, just the bare form) is intentionally NOT
// recognised as a form opener — authors who write a bare
// `[[form]]` token mid-text (in a heading, a sentence,
// etc.) get the literal text preserved, not a phantom
// form block. The form block construct always carries at
// least one attribute pair in wikidot's spec, so
// requiring whitespace after `[[form` is consistent
// with the documented convention.
func isAtFormOpener(source string, i int) bool {
	// `[[form` is exactly 6 chars; we need at least one
	// more char after to discriminate `[[form` from
	// `[[formula]]`. The byte right after the opener is
	// at offset `i + 6`.
	if i+6 >= len(source) {
		return false
	}
	if !strings.HasPrefix(source[i:], "[[form") {
		return false
	}
	after := i + 6
	// Require a whitespace char (space, tab, newline,
	// CR). Other chars (`]`, `=`, `>`, `/`) would be
	// ambiguous with a wiki-link opener (`[[form|alias]]`,
	// for example — though we don't currently support
	// the pipe alias form on form either, but rejecting
	// these keeps the token boundary strict).
	switch source[after] {
	case ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

// indexOfFormOpener returns the byte position of the next
// `[[form` literal that is followed by either whitespace,
// `=`, `>`, or `]]` (so `[[formula` or `[[formation]]` are
// not mistaken for a form opener). We check the byte right
// after `[[form` to disambiguate.
func indexOfFormOpener(source string, from int) int {
	const tok = "[[form"
	idx := from
	for idx < len(source) {
		j := strings.Index(source[idx:], tok)
		if j < 0 {
			return -1
		}
		at := idx + j
		after := at + len(tok)
		if after >= len(source) {
			return -1
		}
		// The next char must be whitespace — bare `[[form]]`
		// (no attrs) is intentionally NOT a form opener; an
		// author who writes a literal `[[form]]` token in
		// a heading or sentence keeps the literal text
		// visible. This matches `isAtFormOpener`.
		switch source[after] {
		case ' ', '\t', '\n', '\r':
			return at
		}
		idx = at + 1
	}
	return -1
}

// extractFormAttrs pulls the attribute tail out of `[[form …]]`
// and returns it as a string ready to splice into the `<form>`
// tag (e.g. ` method="post" action="/api/submit"`). The
// opening `[[form` and trailing `]]` are dropped. Empty string
// when the author wrote a bare `[[form]]` with no attributes.
//
// We sanity-check each `key="value"` pair through
// `sanitizeAnchorID` (key) and HTML escape (value) so the
// resulting `<form>` tag is safe to drop in. Unknown keys are
// passed through (CSS-style attributes with custom names are
// the author's choice; the CSP front-end enforces policy on
// the runtime side).
func extractFormAttrs(opener string) string {
	// opener is `[form …]`. Strip the opener/closer.
	const openTok = "[[form"
	const closeTok = "]]"
	if !strings.HasPrefix(opener, openTok) {
		return ""
	}
	rest := opener[len(openTok):]
	if !strings.HasSuffix(rest, closeTok) {
		return ""
	}
	rest = rest[:len(rest)-len(closeTok)]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}
	// Validate each `key="value"`. We re-use reWDImageAttr
	// (it's the standard `key="value"` regex the wikidot
	// parser uses everywhere). Anything not matching the
	// regex pair is dropped (the author sees only what they
	// wrote, minus the bad pair, since we replace-on-match).
	var out []string
	for _, m := range reWDImageAttr.FindAllStringSubmatch(rest, -1) {
		key := sanitizeAnchorID(m[1])
		val := html.EscapeString(m[2])
		if key == "" || val == "" {
			continue
		}
		out = append(out, fmt.Sprintf(` %s="%s"`, key, val))
	}
	return strings.Join(out, "")
}

// substituteFormWidgets walks a `[[form …]]…[[/form]]` body
// and replaces each form-widget construct (input / textarea /
// button / checkbox / radio / select / option) with the
// matching HTML form element. Prose between widgets is run
// through `inlineOnly` so wikidot inline formatting survives.
//
// Nested `[[form …]]…[[/form]]` blocks (an inner form inside
// the outer form body) are processed by RE-INVOKING
// renderWikidotFormBlocks on the body. The outer render pass
// matched only one `[`[`form…`]`]`-`[`/`form`]` pair, but the
// BODY may contain a nested form which needs its own
// processing; running renderWikidotFormBlocks recursively
// handles that.
//
// Strategy: substitute each PAIR (opener + close) in TWO
// passes — first the openers, then the closes. This avoids
// the closure-captured-body pitfall (ReplaceAllStringFunc
// only replaces the matched span, leaving text AFTER the
// match untouched — so capturing the body and slicing beyond
// the match double-emits the inner content). After both
// passes the body has plain HTML form elements with the
// original prose between them, and inlineOnlyProse only has
// to walk plain HTML+text.
//
// Order of substitution:
//  1. Nested form blocks first (recursive call) — otherwise
//     a `[[form …]]` inside the body would survive to the
//     widget passes and confuse them.
//  2. Single-tag widgets (input / checkbox / radio /
//     form-button) — their `[[input …]]` opener also matches
//     when followed by another `]]` in the source, so
//     substituting them first removes that ambiguity.
//  3. `<textarea>` opener → `<textarea …>` and
//     `[[/textarea]]` → `</textarea>` as separate passes.
//  4. `<select>` and `<option>` follow the same pattern.
//  5. inlineOnlyProse on the resulting body.
func substituteFormWidgets(body string) string {
	// Recurse into any nested form blocks first. We
	// re-invoke renderWikidotFormBlocks (the depth-
	// counter walker) on the body — it can find and
	// process a nested `[[form …]]…[[/form]]` while
	// leaving non-form prose intact.
	body = renderWikidotFormBlocks(body)
	body = reWDInput.ReplaceAllStringFunc(body, func(s string) string { return renderInputTag(s) })
	body = reWDCheckbox.ReplaceAllStringFunc(body, func(s string) string { return renderCheckboxTag(s) })
	body = reWDRadio.ReplaceAllStringFunc(body, func(s string) string { return renderRadioTag(s) })
	body = reWDFormButton.ReplaceAllStringFunc(body, func(s string) string { return renderButtonTag(s) })
	body = reWDTextareaOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDTextareaOpen.FindStringSubmatch(s)
		return "<textarea" + m[1] + ">"
	})
	body = reWDTextareaClose.ReplaceAllString(body, "</textarea>")
	body = reWDSelectOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDSelectOpen.FindStringSubmatch(s)
		return "<select" + m[1] + ">"
	})
	body = reWDSelectClose.ReplaceAllString(body, "</select>")
	body = reWDOptionOpen.ReplaceAllStringFunc(body, func(s string) string {
		m := reWDOptionOpen.FindStringSubmatch(s)
		return "<option" + m[1] + ">"
	})
	body = reWDOptionClose.ReplaceAllString(body, "</option>")
	body = inlineOnlyProse(body)
	return body
}

// inlineOnlyProse is a pass-through to inlineOnly that's used
// as the form-body default — split into its own helper so
// future tweaks (e.g. escape sensitive characters differently
// inside forms) have a clean insertion point.
func inlineOnlyProse(s string) string {
	return inlineOnly(s)
}

// renderInputTag turns `[[input …]]` into `<input … />`. The
// `type` defaults to `text` when omitted (matches HTML
// forms' default). Unknown attributes (e.g. `placeholder=`,
// `pattern=`, `required`) are passed through after
// sanitisation so authors can wire up the full HTML5 input
// surface without wikidot having to learn every new key.
func renderInputTag(s string) string {
	m := reWDInput.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	if attrs["type"] == "" {
		attrs["type"] = "text"
	}
	return buildSelfClosingWidget("input", attrs)
}

// renderCheckboxTag turns `[[checkbox …]]` into
// `<input type="checkbox" … />`. A bare `checked` attribute
// (no value) is preserved on the element so the box renders
// pre-checked. `checked="false"` is dropped (the absence of
// the attribute is the "unchecked" state).
func renderCheckboxTag(s string) string {
	m := reWDCheckbox.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	attrs["type"] = "checkbox"
	return buildSelfClosingWidget("input", attrs)
}

// renderRadioTag turns `[[radio …]]` into `<input type="radio" … />`.
// Same posture as checkbox — `checked` is preserved when present.
func renderRadioTag(s string) string {
	m := reWDRadio.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	attrs["type"] = "radio"
	return buildSelfClosingWidget("input", attrs)
}

// renderButtonTag turns `[[button …]]` into `<button …>label</button>`
// where `label` is taken from the `label="…"` attribute (the
// wikidot convention — HTML <button> uses inner text, not
// a label attribute). When `label` is omitted we render the
// button with the literal text "Submit" as a sensible default.
func renderButtonTag(s string) string {
	m := reWDFormButton.FindStringSubmatch(s)
	attrs := parseWidgetAttrs(m[1])
	label := attrs["label"]
	if label == "" {
		label = "Submit"
	}
	delete(attrs, "label")
	if attrs["type"] == "" {
		attrs["type"] = "submit"
	}
	return buildPairedWidget("button", attrs, label)
}

// renderTextareaTag turns `[[textarea attrs]]content[[/textarea]]`
// into `<textarea attrs>content</textarea>`. The body is
// HTML-escaped at substitution time but already went through
// inlineOnly first so links / bold become real HTML before
// being inserted (no double-escape).
func renderTextareaTag(attrs string, inner string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("textarea", a, inner)
}

// renderSelectTag turns `[[select attrs]]…options…[[/select]]`
// into `<select attrs>…options…</select>`. The inner prose
// has already been substituted by the parent loop, so we
// just wrap it.
func renderSelectTag(attrs string, inner string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("select", a, inner)
}

// renderOptionTag turns `[[option attrs]]label[[/option]]`
// into `<option attrs>label</option>`. The label is run
// through inlineOnly by the outer loop so wikidot inline
// formatting (e.g. `//italic//` for an option label)
// survives. We do NOT entity-escape here — the upstream
// inlineOnly pass is already safe.
func renderOptionTag(attrs string, label string) string {
	a := parseWidgetAttrs(attrs)
	return buildPairedWidget("option", a, label)
}

// parseWidgetAttrs parses the attribute tail shared by every
// form widget (`[[TAG attrs]]`). Keys are lowercased; values
// are HTML-escaped; bare keys (HTML5 boolean attributes like
// `checked`, `selected`, `required`, `disabled`, `autofocus`)
// are recorded with an empty value so the renderer knows to
// emit them as bare attributes.
//
// Unknown keys (`placeholder`, `pattern`, `min`, `max`,
// `step`, `data-*`, etc.) pass through sanitisation
// unchanged — that's by design, since wikidot doesn't want
// to maintain an allow-list of every HTML5 attribute.
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

func renderWikidotTable(p *wikidotParser, raw string) string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var sb strings.Builder
	sb.WriteString(`<table class="wiki-table"><tbody>`)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tag := "td"
		if i == 0 {
			tag = "th"
		}
		sb.WriteString("<tr>")
		for _, cell := range strings.Split(line, "|") {
			c := strings.TrimSpace(cell)
			// Inline-only — don't wrap cells in <p>.
			sb.WriteString(fmt.Sprintf("<%s>%s</%s>", tag, inlineOnly(c), tag))
		}
		sb.WriteString("</tr>")
	}
	sb.WriteString("</tbody></table>")
	return sb.String()
}

// ── inlineOnly ─────────────────────────────────────────────────────────

// inlineVars is the package-level shadow of the current
// render's `%%var%%` substitution table. The wikidotParser sets
// it in convert() before any inline pass and clears it after —
// inlineOnly then reads it without the parser-instance plumbing
// (which would otherwise force every list/table/span callback
// to thread the parser pointer through). The shadow is
// protected by a mutex so concurrent renders (each with their
// own parser instance) don't trample each other. Cost: one
// mutex acquire per inline pass per render — negligible
// against the surrounding regex work.
var (
	inlineVarsMu sync.Mutex
	inlineVars   map[string]string
)

func (p *wikidotParser) setInlineVars() {
	inlineVarsMu.Lock()
	inlineVars = p.vars
	inlineVarsMu.Unlock()
}

func (p *wikidotParser) clearInlineVars() {
	inlineVarsMu.Lock()
	inlineVars = nil
	inlineVarsMu.Unlock()
}

// inlineOnly applies inline formatting to text (no block elements).
func inlineOnly(text string) string {
	// Apply %%var%% substitution against the current render's
	// shadow table (set by the calling wikidotParser.convert()).
	text = reWDVar.ReplaceAllStringFunc(text, func(s string) string {
		inlineVarsMu.Lock()
		vars := inlineVars
		inlineVarsMu.Unlock()
		if len(vars) == 0 {
			return s
		}
		m := reWDVar.FindStringSubmatch(s)
		if v, ok := vars[m[1]]; ok {
			return html.EscapeString(v)
		}
		return s
	})
	text = reWDBold.ReplaceAllString(text, `<strong>$1</strong>`)
	// See note on the package-level reWDItalic — same fix applied here
	// so a Phase-9 list / Phase-1g span / Phase-1e table call back into
	// inlineOnly on text containing already-wrapped auto-link URLs
	// doesn't insert a stray <em> in the middle of an <a href>.
	text = reWDItalic.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDItalic.FindStringSubmatch(s)
		return m[1] + "<em>" + m[2] + "</em>"
	})
	text = reWDUnderline.ReplaceAllString(text, `<u>$1</u>`)
	text = reWDStrikethrough.ReplaceAllString(text, `<s>$1</s>`)
	text = reWDSuperscript.ReplaceAllString(text, `<sup>$1</sup>`)
	text = reWDSubscript.ReplaceAllString(text, `<sub>$1</sub>`)
	text = reWDInlineCode.ReplaceAllString(text, `<code>$1</code>`)
	text = reWDInlineColor.ReplaceAllStringFunc(text, applyWikidotInlineColor)
	// Inline footnote refs in list items / table cells.
	text = reWDFootnoteRef.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDFootnoteRef.FindStringSubmatch(s)
		// No parser-context access from inlineOnly — we
		// conservatively render all numeric `[N]` as a generic
		// footnote-ref back-link. The parser's main pass will
		// already have replaced body refs with the real id;
		// this is just defensive.
		n := m[1]
		return fmt.Sprintf(`<sup class="footnote-ref"><a href="#fn-%s">%s</a></sup>`, n, html.EscapeString(n))
	})
	text = reWDExternalLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDExternalLink.FindStringSubmatch(s)
		url := strings.TrimSpace(m[1])
		display := strings.TrimSpace(m[2])
		if display == "" {
			display = url
		}
		if safe := sanitizeURLForAttr(url); safe != "" {
			return fmt.Sprintf(`<a href="%s" rel="nofollow noopener" target="_blank">%s</a>`, safe, html.EscapeString(display))
		}
		return html.EscapeString(display)
	})
	text = reWDMailto.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDMailto.FindStringSubmatch(s)
		addr := strings.TrimSpace(m[1])
		display := strings.TrimSpace(m[2])
		if display == "" {
			display = addr
		}
		if safe := sanitizeURLForAttr("mailto:" + addr); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(display))
		}
		return html.EscapeString(display)
	})
	text = reWDWikiLink.ReplaceAllStringFunc(text, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		href := m[1]
		text := m[1]
		if m[2] != "" {
			text = m[2]
		}
		if !strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			href = "/wikidot/" + href
		}
		if safe := sanitizeURLForAttr(href); safe != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, safe, html.EscapeString(text))
		}
		return html.EscapeString(text)
	})
	return text
}

// ── blockquote / list rendering ─────────────────────────────────────────

// renderWikidotAdvancedLists walks the source for
// `[[ul]]` / `[[ol]]` / `[[li]]` / `[[/li]]` / `[[/ul]]`
// / `[[/ol]]` blocks, stashes the rendered HTML as a
// single block placeholder, and returns the source with
// each match replaced. We use a single FindAllSubmatch
// pass to collect every open/close token (with positions),
// then a stack-based walker to emit well-formed nested
// `<ul>`/`<ol>` HTML. Attributes (`class`, `style`,
// `data-...`) are forwarded from the open-tag tail.
//
// The walker is robust to author errors (unclosed
// `[[ul]]` is closed implicitly at the end of the
// matched region; lone `[[/li]]` is ignored). Nested
// `[[ul]]` / `[[ol]]` inside a `[[li]]` body is fully
// supported.
func renderWikidotAdvancedLists(text string) string {
	// Tokenise: collect every (position, kind, attrs) entry
	// in source order. A "kind" is one of "ul", "ol", "li",
	// "/li", "/ul", "/ol". The walker then visits tokens
	// left-to-right.
	toks := []advListTok{}
	for _, m := range reWDUL.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "ul", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDOL.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "ol", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDLIOpen.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "li", text[m[0]:m[1]], text[m[2]:m[3]]})
	}
	for _, m := range reWDLIClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/li", text[m[0]:m[1]], ""})
	}
	for _, m := range reWDULClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/ul", text[m[0]:m[1]], ""})
	}
	for _, m := range reWDOLClose.FindAllStringSubmatchIndex(text, -1) {
		toks = append(toks, advListTok{m[0], m[1], "/ol", text[m[0]:m[1]], ""})
	}
	// Sort by position (stable order is needed because
	// FindAllSubmatchIndex returns results in
	// source-order, but mixing multiple FindAll calls
	// across the same source keeps that order as long
	// as the source is unchanged).
	sortToks(toks)
	if len(toks) == 0 {
		return text
	}

	// Walk tokens. Each `[[ul]]` / `[[ol]]` opens a new
	// list level; each `[[li]]` opens an item; `[[/li]]`,
	// `[[/ul]]`, `[[/ol]]` close. We collect rendered
	// spans per top-level match range (start, end), then
	// replace those ranges in the source.
	type listFrame struct {
		tag   string // "ul" or "ol"
		attrs string
		open  bool // has the `<tag>` been emitted?
	}
	type itemFrame struct {
		attrs   string
		open    bool
		hasBody bool // at least one body segment written
	}
	var sb strings.Builder
	last := 0
	// Stack of frames. The top of the stack is the
	// innermost open list; under it is the parent list
	// (or the parent item). Each frame also tracks
	// whether the parent is a list or an item.
	type frameKind int
	const (
		frameList frameKind = iota
		frameItem
	)
	type frame struct {
		kind  frameKind
		tag   string // "ul" / "ol" for lists, "" for items
		attrs string
	}
	var stack []frame
	flushItem := func() {
		// Close the innermost open item, if any.
		// An item is "open" if its frame is on
		// the stack — the stack itself tracks
		// open/close state, so we just need to
		// check the top-of-stack kind. (Earlier
		// versions used `attrs != ""` as a
		// proxy for "open" which broke bare
		// `[[li]]` items because they have no
		// attributes.)
		if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
			sb.WriteString("</li>")
		}
	}
	flushList := func() {
		// Close one list level.
		if len(stack) > 0 && stack[len(stack)-1].kind == frameList {
			sb.WriteString("</")
			sb.WriteString(stack[len(stack)-1].tag)
			sb.WriteString(">")
		}
	}
	for _, t := range toks {
		// Text between the previous token and this one
		// is body content. We append it to the
		// innermost item (if any) as inlineOnly'd
		// HTML.
		if t.pos > last {
			body := text[last:t.pos]
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				sb.WriteString(inlineOnly(body))
			} else {
				// Body outside any list — pass
				// through (the caller wraps it).
				sb.WriteString(body)
			}
		}
		switch t.kind {
		case "ul", "ol":
			// A new list opens NESTED inside any
			// currently-open item (HTML allows
			// <ul> as a child of <li>), so we
			// do NOT flush the current item
			// here. We only flush a previous
			// list of a DIFFERENT marker (mixing
			// `[[ul]]` and `[[ol]]` at the same
			// level closes the previous one and
			// opens a new one) — that's the only
			// case where a list-open also ends a
			// list.
			if len(stack) > 0 && stack[len(stack)-1].kind == frameList && stack[len(stack)-1].tag != t.kind {
				// Close any open item in the
				// previous list before closing
				// the list itself.
				flushItem()
				flushList()
				stack = stack[:len(stack)-1]
			}
			attrs := parseCollapsibleAttrs(t.attrs)
			style := styleFromAttrs(attrs)
			cls := classFromAttrs(attrs)
			extra := ""
			if cls != "" {
				extra += " class=\"" + cls + "\""
			}
			if style != "" {
				extra += " style=\"" + style + "\""
			}
			sb.WriteString("<")
			sb.WriteString(t.kind)
			sb.WriteString(">")
			if extra != "" {
				// Inject into the open tag — rewind
				// the just-written `<tag>` and
				// re-emit with attributes. We keep
				// the leading `<` so the rewound
				// length is `len(t.kind)+1` (the
				// tag chars + the closing `>`),
				// not `len(t.kind)+2` (which
				// would eat the `<` too).
				written := sb.String()
				sb.Reset()
				sb.WriteString(written[:len(written)-len(t.kind)-1])
				sb.WriteString(t.kind)
				sb.WriteString(extra)
				sb.WriteString(">")
			}
			stack = append(stack, frame{kind: frameList, tag: t.kind, attrs: t.attrs})
		case "li":
			// Close any currently-open item first.
			flushItem()
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				// Nested without an item close —
				// ignore (the author wrote
				// `[[li]][[li]]` which is invalid).
				continue
			}
			if len(stack) == 0 || stack[len(stack)-1].kind == frameList {
				// Implicit list start: emit a
				// `<ul>` so the `<li>` is
				// well-formed.
				if len(stack) == 0 {
					sb.WriteString("<ul>")
					stack = append(stack, frame{kind: frameList, tag: "ul", attrs: ""})
				}
			}
			attrs := parseCollapsibleAttrs(t.attrs)
			style := styleFromAttrs(attrs)
			cls := classFromAttrs(attrs)
			extra := ""
			if cls != "" {
				extra += " class=\"" + cls + "\""
			}
			if style != "" {
				extra += " style=\"" + style + "\""
			}
			sb.WriteString("<li")
			sb.WriteString(extra)
			sb.WriteString(">")
			stack = append(stack, frame{kind: frameItem, attrs: t.attrs})
		case "/li":
			flushItem()
			if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
				stack = stack[:len(stack)-1]
			}
		case "/ul", "/ol":
			// Close any open item first.
			flushItem()
			// Pop frames until we find a matching list.
			tag := t.kind[1:] // strip leading "/"
			popped := false
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].kind == frameList && stack[i].tag == tag {
					// Pop everything up to and
					// including this list.
					for j := len(stack) - 1; j >= i; j-- {
						if stack[j].kind == frameItem {
							sb.WriteString("</li>")
						} else {
							sb.WriteString("</")
							sb.WriteString(stack[j].tag)
							sb.WriteString(">")
						}
					}
					stack = stack[:i]
					popped = true
					break
				}
			}
			if !popped {
				// Unmatched close — ignore.
			}
		}
		last = t.end
	}
	// Trailing text after the last token.
	if last < len(text) {
		body := text[last:]
		if len(stack) > 0 && stack[len(stack)-1].kind == frameItem {
			sb.WriteString(inlineOnly(body))
		} else {
			sb.WriteString(body)
		}
	}
	// Close any frames still on the stack.
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].kind == frameItem {
			sb.WriteString("</li>")
		} else {
			sb.WriteString("</")
			sb.WriteString(stack[i].tag)
			sb.WriteString(">")
		}
	}
	// Splice the rendered list block into the source
	// wherever a top-level list was found. We do this
	// by walking the source again and replacing each
	// `[[ul]]` (or `[[ol]]`) with the rendered output.
	// Since we only have one rendered string, we use
	// the simple approach: the advanced-list region is
	// the smallest span covering every advanced-list
	// token, and the rendered output replaces that
	// span verbatim.
	if len(toks) == 0 {
		return text
	}
	first := toks[0].pos
	lastEnd := toks[0].end
	for _, t := range toks[1:] {
		if t.pos > lastEnd {
			// Non-contiguous — emit the
			// currently-built list, then
			// preserve the gap, then start a
			// new list region.
			// (We don't currently handle this
			// case; advanced-list authors
			// always write contiguous blocks.)
		}
		if t.end > lastEnd {
			lastEnd = t.end
		}
	}
	// We always produce exactly ONE rendered block per
	// pass and replace the entire token span. This
	// handles the common case where one article has
	// multiple separate advanced-list blocks only if
	// they don't interleave; if they do, the rendered
	// output is concatenated into one block. We
	// accept that limitation for the spec round 2
	// scope.
	return text[:first] + sb.String() + text[lastEnd:]
}

// sortToks is a tiny insertion sort used by
// renderWikidotAdvancedLists to keep token positions
// in source order (the multiple FindAllStringSubmatchIndex
// scans are returned in source order individually, but
// their union is a merge that needs an explicit sort).
func sortToks(a []advListTok) {
	for i := 1; i < len(a); i++ {
		v := a[i]
		j := i - 1
		for j >= 0 && a[j].pos > v.pos {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = v
	}
}

// styleFromAttrs / classFromAttrs extract the style /
// class keys from a parsed attribute map (used by
// renderWikidotAdvancedLists). We split these out
// because the map-merge logic in renderWikidotAdvancedLists
// builds up attributes from `data-*` keys too, and we
// want to keep the in-tag attribute order stable
// (class, style, data-…) for predictable test output.
func styleFromAttrs(attrs map[string]string) string {
	s, ok := attrs["style"]
	if !ok {
		return ""
	}
	return sanitizeCSSValue(s)
}

func classFromAttrs(attrs map[string]string) string {
	c, ok := attrs["class"]
	if !ok {
		return ""
	}
	return sanitizeAnchorID(c)
}

// slugifyUsername converts a wikidot user-mention name
// to a URL-safe slug. Wikidot usernames can contain
// spaces and any printable character; the user-profile
// route (`/user/<slug>`) should resolve by an
// un-ambiguous, lowercased slug. We allow `[A-Za-z0-9_-]`
// and map everything else to `-`, then collapse runs of
// `-` and trim leading / trailing `-`. The result is
// safe to drop into an `<a href="...">` (no `<`, `>`,
// `"` or `'` survives) without an additional pass.

// mapWikidotDateFormat accepts either a Go time-format
// string or a Wikidot-style format string (the latter
// uses `$YYYY` / `$MM` / `$DD` / `$HH` etc.). Wikidot
// migrated articles tend to use the Wikidot form; we
// translate the documented tokens to Go's layout so
// the renderer doesn't silently produce garbage on
// legacy pages. Anything not matching a documented
// token is returned unchanged (Go's `time.Format` will
// pass through unknown literals).
//
// renderWikidotIndentBlocks scans `source` left-to-right
// for `[[indent]]` open tags, walks past any nested
// `[[indent]]` opens (counting depth), and replaces each
// matched `[[indent]]…[[/indent]]` block with the
// rendered `<div class="wikidot-indent">`. The depth
// counter is what makes nested indents work — a regex
// can't count opens, so a non-greedy `.*?` between the
// open and close tags would consume the FIRST inner
// `[[/indent]]` it sees, breaking the outer block.
//
// Unmatched `[[indent]]` (no close anywhere downstream)
// is left as raw text so the author can see the typo.
func renderWikidotIndentBlocks(source string, p *wikidotParser) string {
	const open = "[[indent]]"
	const close = "[[/indent]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		oi := strings.Index(source[i:], open)
		if oi < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit the prefix up to the open tag.
		sb.WriteString(source[i : i+oi])
		blockStart := i + oi + len(open)
		// Walk from blockStart, counting nested
		// opens until we find the matching close.
		depth := 1
		j := blockStart
		for j < len(source) {
			nextOpen := strings.Index(source[j:], open)
			nextClose := strings.Index(source[j:], close)
			if nextClose < 0 {
				// Unmatched — leave the
				// open tag raw, plus
				// everything after it (the
				// author can see what they
				// wrote).
				sb.WriteString(source[i+oi:])
				return sb.String()
			}
			if nextOpen >= 0 && nextOpen < nextClose {
				depth++
				j += nextOpen + len(open)
				continue
			}
			depth--
			closeEnd := j + nextClose + len(close)
			if depth == 0 {
				body := source[blockStart : j+nextClose]
				// `inlineOnly` keeps inline
				// formatting (bold / italic /
				// links) but doesn't emit
				// block-level wrappers. The
				// newline-to-`<br />` rewrite
				// lets multi-line bodies
				// render without dragging a
				// `<p>` into the indent div.
				// First re-process the body
				// so nested `[[indent]]`
				// blocks become their own
				// `<div>`s — otherwise a
				// nested indent would stay
				// as raw text inside the
				// outer body. We recurse via
				// `renderWikidotIndentBlocks`
				// first; if the body has
				// nested indents they'll
				// come out as `<div>`s, and
				// the surrounding inline
				// text gets the newline
				// rewrite.
				preNested := renderWikidotIndentBlocks(body, p)
				inner := strings.TrimSpace(inlineOnly(preNested))
				inner = strings.ReplaceAll(inner, "\n", "<br />")
				sb.WriteString(`<div class="wikidot-indent">`)
				sb.WriteString(inner)
				sb.WriteString(`</div>`)
				i = closeEnd
				goto nextBlock
			}
			j = closeEnd
		}
		// Reached EOF without finding a matching
		// close at depth 1 — leave the open raw.
		sb.WriteString(source[i+oi:])
		return sb.String()
	nextBlock:
	}
	return sb.String()
}

// renderWikidotTabviews walks `source` left-to-right,
// matching `[[tabview]]…[[/tabview]]` blocks via a
// depth-counter (so a nested `[[tabview]]` inside a
// tab body doesn't confuse the outer match), then
// splits each matched body by `[[tab Title]]…[[/tab]]`
// entries. Output is a `.wikidot-tabview` container
// with:
//   - `<ul class="wikidot-tab-nav">` listing each
//     tab as a `<li class="wikidot-tab-tab">` (the
//     first tab gets `.active`)
//   - `<div class="wikidot-tab-panels">` listing
//     each panel as `<div class="wikidot-tab-panel">`
//     (the first panel gets `.active`)
//
// Each tab and panel share a `data-tab-id="N"`
// attribute (N is the 0-based index) so the
// client-side script (ArticleView.tsx) can match
// nav clicks to panel visibility without re-parsing
// the DOM.
//
// Tab titles are HTML-escaped (they're plain text,
// not wikidot source). Tab bodies are routed through
// `convertNoFootnote` so block-level markup inside
// (lists, blockquotes, code blocks, even nested
// tabviews) still renders — but a tab can't spawn
// its own footnote list (Wikidot's behaviour;
// footnote lists are article-scoped).
//
// renderWikidotSizeBlocks walks `source` left-to-right,
// matching `[[size …]]…[[/size]]` blocks via a depth
// counter (so a nested `[[size …]]` inside the inner
// body doesn't trip the outer close). The recursive
// structure mirrors renderWikidotIndentBlocks.
//
// Each block's body is itself run through this same
// function (via `renderWikidotSizeBlocks(inner)`), so
// nested `[[size …]]` blocks become nested `<span
// style="font-size:…">` wrappers.
//
// Two valid value classes:
//
//  1. Whitelisted keyword (`xx-small`, `larger`, etc. —
//     listed in `sizeMap`); the keyword is converted to
//     a rem value for portability.
//  2. Whitelisted numeric form (`N`, `Npx`, `Nem`,
//     `N%`, with optional fractional part). The regex
//     `reWDSizeValue` validates the pattern; values
//     outside the whitelist degrade to plain text (no
//     `<span style="font-size:giant">` typo-leak).
//
// Anything outside both classes (e.g. `[[size giant]]`,
// `[[size huger]]`) keeps the construct verbatim so the
// author sees the typo.
func renderWikidotSizeBlocks(source string) string {
	const close = "[[/size]]"
	// `reWDSizeOpen` matches only the opener portion of a
	// wikidot size construct — the size value but not the
	// body / close. We need this separate regex because
	// `reWDSize` greedily matches the full construct
	// (`opener … body … close`), which would make the
	// depth-counter below think the opener end is also the
	// close end.
	reWDSizeOpen := regexp.MustCompile(`(?is)\[\[size\s+[^\]]+\]\]`)
	var sb strings.Builder
	i := 0
	for i < len(source) {
		// Find next opener at-or-after i.
		loc := reWDSizeOpen.FindStringIndex(source[i:])
		if loc == nil {
			sb.WriteString(source[i:])
			return sb.String()
		}
		openerStart := i + loc[0]
		openerEnd := i + loc[1]
		// Emit everything verbatim up to the opener.
		sb.WriteString(source[i:openerStart])
		// Extract the opener's size value. We re-match
		// just the opener so we capture only the size
		// token (group 1), not the body of a nested
		// construct.
		m := reWDSizeOpen.FindStringSubmatch(source[openerStart:])
		if m == nil {
			// Defensive — shouldn't fail here since
			// FindStringIndex returned a hit. Emit
			// the opener verbatim and advance.
			sb.WriteString(source[openerStart:openerEnd])
			i = openerEnd
			continue
		}
		// m[0] is the entire opener (e.g. `[[size 0]]`).
		// The size value is between `[[size ` and `]]`.
		// Trim both ends rather than using capture group
		// 1 because the opener regex above doesn't expose
		// a capture — only m[0] is the full match.
		css := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(m[0], "[[size "), "]]"))
		cssAttr, isValid := resolveSizeCss(css)
		// Depth-counted walk for the matching `[[/size]]`.
		j := openerEnd
		depth := 1
		for j < len(source) {
			nextOpen := reWDSizeOpen.FindStringIndex(source[j:])
			nextClose := strings.Index(source[j:], close)
			switch {
			case nextClose < 0:
				// No close — leave the opener raw.
				sb.WriteString(source[openerStart:])
				return sb.String()
			case nextOpen != nil && (j+nextOpen[0]) < (j+nextClose):
				depth++
				j = j + nextOpen[1]
			default:
				depth--
				closeAt := j + nextClose
				closeEnd := closeAt + len(close)
				if depth == 0 {
					body := source[openerEnd:closeAt]
					bodyRendered := renderWikidotSizeBlocks(body)
					if isValid {
						// Size across paragraph boundaries
						// would produce a <span> that
						// straddles the boundary — Phase 11
						// (paragraph wrap) then wraps each
						// chunk in its own <p> and the
						// outer </span> ends up in the
						// wrong paragraph. We close the
						// span at every `\n\n` and reopen
						// after, so each paragraph in the
						// rendered HTML is individually
						// wrapped in its own <span
						// style="font-size:X">…</span>
						// (Phase 11 still wraps each
						// chunk in <p>, which then sits
						// cleanly around the per-paragraph
						// span).
						if strings.Contains(bodyRendered, "\n\n") {
							chunks := strings.Split(bodyRendered, "\n\n")
							for k, chunk := range chunks {
								if k > 0 {
									sb.WriteString("\n\n")
								}
								if chunk == "" {
									continue
								}
								sb.WriteString(`<span style="font-size:`)
								sb.WriteString(cssAttr)
								sb.WriteString(`">`)
								sb.WriteString(chunk)
								sb.WriteString(`</span>`)
							}
						} else {
							sb.WriteString(`<span style="font-size:`)
							sb.WriteString(cssAttr)
							sb.WriteString(`">`)
							sb.WriteString(bodyRendered)
							sb.WriteString(`</span>`)
						}
					} else {
						sb.WriteString(bodyRendered)
					}
					i = closeEnd
					goto nextSize
				}
				j = closeEnd
			}
		}
		// EOF without a matching close — leave raw.
		sb.WriteString(source[openerStart:])
		return sb.String()
	nextSize:
	}
	return sb.String()
}

// resolveSizeCss returns the canonical CSS value for a
// wikidot size token. If the token is a known keyword
// (mapped via `sizeMap`), the rem value is returned with
// `isValid=true`. If the token is a numeric form (`Npx` /
// `Nem` / `N%` / plain `N`), the raw token is returned
// with `isValid=true`. Anything else (including bogus
// keywords like `giant`, or CSS values that bypassed
// `reWDSizeValue`'s whitelist) returns `isValid=false`
// so the caller falls back to the body-only path.
func resolveSizeCss(token string) (string, bool) {
	if v, ok := sizeMap[strings.ToLower(token)]; ok {
		return v, true
	}
	if !reWDSizeValue.MatchString(token) {
		return "", false
	}
	return token, true
}

// renderWikidotTabviews walks `source` left-to-right,
// matching `[[tabview]]…[[/tabview]]` blocks via a
// depth-counter (so a nested `[[tabview]]` inside a tab
// (silent) mistake.
func renderWikidotTabviews(source string, p *wikidotParser) string {
	const open = "[[tabview]]"
	const close = "[[/tabview]]"
	var sb strings.Builder
	i := 0
	for i < len(source) {
		oi := strings.Index(source[i:], open)
		if oi < 0 {
			sb.WriteString(source[i:])
			return sb.String()
		}
		// Emit the prefix up to the tabview opener.
		sb.WriteString(source[i : i+oi])
		blockStart := i + oi + len(open)
		// Walk from blockStart, counting nested
		// `[[tabview]]` opens (so an inner opener
		// doesn't trip the depth back to 0).
		depth := 1
		j := blockStart
		for j < len(source) {
			nextOpen := strings.Index(source[j:], open)
			nextClose := strings.Index(source[j:], close)
			if nextClose < 0 {
				// Unmatched — emit the
				// opener raw, plus
				// everything after.
				sb.WriteString(source[i+oi:])
				return sb.String()
			}
			if nextOpen >= 0 && nextOpen < nextClose {
				depth++
				j += nextOpen + len(open)
				continue
			}
			depth--
			closeEnd := j + nextClose + len(close)
			if depth == 0 {
				body := source[blockStart : j+nextClose]
				// Recurse so a nested
				// `[[tabview]]` inside a
				// tab body gets rendered
				// as its own container,
				// not as raw text.
				preNested := renderWikidotTabviews(body, p)
				tabs := reWDTabItem.FindAllStringSubmatch(preNested, -1)
				if len(tabs) == 0 {
					// No well-formed
					// `[[tab …]]…[[/tab]]`
					// children. Two sub-
					// cases:
					//   1. body has a
					//      `[[tab ` opener
					//      with no matching
					//      `[[/tab]]` —
					//      leave the
					//      opener raw so
					//      the author can
					//      see the typo.
					//   2. body is empty
					//      or has no
					//      `[[tab ` at all
					//      — emit an empty
					//      container.
					if strings.Contains(preNested, "[[tab ") {
						sb.WriteString(source[i+oi : closeEnd])
					} else {
						sb.WriteString(`<div class="wikidot-tabview"></div>`)
					}
					i = closeEnd
					goto nextTab
				}
				sb.WriteString(`<div class="wikidot-tabview">`)
				sb.WriteString(`<ul class="wikidot-tab-nav">`)
				for idx, t := range tabs {
					title := strings.TrimSpace(t[1])
					if idx == 0 {
						sb.WriteString(`<li class="wikidot-tab-tab active" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`"><a href="#" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
						sb.WriteString(html.EscapeString(title))
						sb.WriteString(`</a></li>`)
					} else {
						sb.WriteString(`<li class="wikidot-tab-tab" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`"><a href="#" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
						sb.WriteString(html.EscapeString(title))
						sb.WriteString(`</a></li>`)
					}
				}
				sb.WriteString(`</ul>`)
				sb.WriteString(`<div class="wikidot-tab-panels">`)
				for idx, t := range tabs {
					content := strings.TrimSpace(p.convertNoFootnote(t[2]))
					if idx == 0 {
						sb.WriteString(`<div class="wikidot-tab-panel active" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
					} else {
						sb.WriteString(`<div class="wikidot-tab-panel" data-tab-id="`)
						sb.WriteString(strconv.Itoa(idx))
						sb.WriteString(`">`)
					}
					sb.WriteString(content)
					sb.WriteString(`</div>`)
				}
				sb.WriteString(`</div>`)
				sb.WriteString(`</div>`)
				i = closeEnd
				goto nextTab
			}
			j = closeEnd
		}
		// Reached EOF without a matching close —
		// emit the opener raw.
		sb.WriteString(source[i+oi:])
		return sb.String()
	nextTab:
	}
	return sb.String()
}

func mapWikidotDateFormat(format string) string {
	if !strings.Contains(format, "$") {
		return format
	}
	// Order matters: longer tokens first so `$YYYY`
	// doesn't get partially replaced by `$YY`. We
	// also handle `$M-$MM` order — the longer token
	// is processed first.
	replacer := strings.NewReplacer(
		"$YYYY", "2006",
		"$YY", "06",
		"$MM", "01",
		"$M", "1",
		"$DD", "02",
		"$D", "2",
		"$HH", "15",
		"$H", "15",
		"$mm", "04",
		"$m", "4",
		"$ss", "05",
		"$s", "5",
	)
	return replacer.Replace(format)
}

func slugifyUsername(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '_' || r == '-':
			b.WriteRune('-')
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		// Edge case: name is all non-alphanumeric
		// (e.g. `[[user +]]`). Fall back to a
		// numeric stub so the link doesn't 404
		// silently — the slug is non-empty and
		// obvious as a placeholder.
		return "user"
	}
	return out
}

// renderWikidotDefList converts a run of `: term : definition`
// lines (and their `:` continuations) into ONE
// `<dl>...</dl>` HTML block. Consecutive `: term : def`
// lines (possibly interleaved with continuation lines)
// share a single `<dl>`. A blank line or any other
// non-def line ends the current `<dl>`.
//
// IMPORTANT: the rendered block is emitted as a SINGLE
// LINE (no internal `\n`) so the wrap phase treats it as
// one block-level element and never wraps the inner
// `<dt>` / `<dd>` in `<p>` tags. We use `\n` only as the
// boundary between the def-list run and the surrounding
// text. The pre-pass at the end of the function
// collapses any `\n\n+` around the `<dl>` back to a
// single newline so the surrounding paragraph
// structure isn't disturbed.
func renderWikidotDefList(text string) string {
	// We walk the source line by line, but the
	// rendered `<dl>…</dl>` is emitted as a single
	// contiguous string with NO internal newlines.
	// Continuation lines append `<br />…` to the
	// previous `<dd>` (re-opening the `<dd>` by
	// stripping the previous `</dd>` and writing a
	// new `…<br />…</dd>`). The result is a single
	// line that the wrap phase recognises as a block
	// (via the `dl` token in `blockInBuf`).
	var sb strings.Builder
	inDL := false
	lastWasDD := false
	flushClose := func() {
		if inDL {
			sb.WriteString("</dl>")
			inDL = false
			lastWasDD = false
		}
	}
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		trimmed := strings.TrimRight(raw, "\r")
		// We emit a `\n` BETWEEN source lines so
		// the rest of the wrap pass sees the
		// original line structure. The exception
		// is INSIDE a `<dl>` block: there the
		// def items must stay on a single line
		// (or the wrap phase will treat each
		// `<dt>` line as a paragraph and wrap
		// them in `<p>`). So we suppress the
		// trailing `\n` after a def item or
		// continuation, and re-emit it only
		// when the next non-def line forces the
		// `<dl>` to close.
		emitNewline := func() {
			if i < len(lines)-1 {
				sb.WriteString("\n")
			}
		}
		if m := reWDDefItem.FindStringSubmatch(trimmed); m != nil {
			if !inDL {
				sb.WriteString("<dl>")
				inDL = true
			}
			term := m[1]
			def := m[2]
			sb.WriteString("<dt>")
			sb.WriteString(inlineOnly(term))
			sb.WriteString("</dt><dd>")
			sb.WriteString(inlineOnly(def))
			sb.WriteString("</dd>")
			lastWasDD = true
			// No trailing `\n` — the next item
			// (or the `</dl>` close) is on the
			// same line.
		} else if inDL && lastWasDD {
			cm := reWDDefCont.FindStringSubmatch(trimmed)
			if cm != nil && !strings.Contains(cm[1], ":") {
				written := sb.String()
				if strings.HasSuffix(written, "</dd>") {
					sb.Reset()
					sb.WriteString(written[:len(written)-len("</dd>")])
				}
				sb.WriteString("<br />")
				sb.WriteString(inlineOnly(cm[1]))
				sb.WriteString("</dd>")
				// No trailing `\n`.
			} else {
				// Non-def line in the middle
				// of a `<dl>` — close the
				// block, emit the line
				// verbatim, and re-emit the
				// `\n` so the surrounding
				// text structure is preserved.
				flushClose()
				sb.WriteString(trimmed)
				emitNewline()
			}
		} else {
			sb.WriteString(trimmed)
			emitNewline()
		}
	}
	flushClose()
	return sb.String()
}

func renderWikidotBlockquotes(text string) string {
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	flush := func() {
		if len(buf) > 0 {
			result = append(result, `<blockquote>`+strings.Join(buf, `<br />`)+`</blockquote>`)
			buf = nil
		}
	}
	for _, line := range lines {
		if m := reWDBlockquote.FindStringSubmatch(line); m != nil {
			// Spec: a `\` at the end of a blockquote
			// line lets the author continue the
			// quote on the next source line, but the
			// rendered output stays as ONE line
			// (i.e. without a `<br />` between).
			// Strip the trailing `\` and any trailing
			// whitespace, then JOIN the next
			// blockquote line into the same buffer
			// entry so the `<br />` joiner above
			// never sees a line break.
			joined := strings.TrimRight(m[1], " \t\\")
			if len(buf) > 0 {
				buf[len(buf)-1] = buf[len(buf)-1] + joined
			} else {
				buf = append(buf, joined)
			}
		} else {
			flush()
			result = append(result, line)
		}
	}
	flush()
	return strings.Join(result, "\n")
}

// renderWikidotLists — rewritten to support nested lists. Each line
// starting with `* ` (unordered) or `# ` (ordered) at a given
// indent depth becomes a <li>; indent is computed as
// `len(leadingSpaces) / 2` (Wikidot's convention is 2 spaces per
// level). Adjacent same-type list items form a <ul>/<ol>; depth
// changes open/close nested lists.
//
// Mixed types (a `* ` line followed by `# ` lines at the same
// level) close the current list and start a new one of the other
// type. This matches Wikidot's own rendering: a depth-1 `#`
// under a depth-1 `*` becomes a child <ol> inside the <li> of
// the surrounding <ul>.
func renderWikidotLists(text string) string {
	// Pre-pass: convert advanced `[[ul]]` / `[[ol]]` /
	// `[[li]]` blocks to a stashed `<ul>/<ol>` block
	// before the line-based `*` / `#` list pass runs.
	// The advanced form is parsed by a stack-based
	// walker (nested `<ul>`/`<ol>` allowed) and the
	// output is a single HTML string that the wrap
	// phase treats as block-level.
	text = renderWikidotAdvancedLists(text)

	// Pre-pass: convert definition-list lines into
	// stashed HTML placeholders. We do this BEFORE the
	// `*` / `#` list pass so a definition list doesn't
	// accidentally trigger the list regex (the leading
	// `:` is not a list marker, but the lines are
	// handled by a separate code path that the list
	// pass can safely ignore). The output is a single
	// `<dl>...</dl>` block that the wrap phase treats
	// as block-level and never wraps in <p>.
	text = renderWikidotDefList(text)

	// Split on newlines but keep blank-line groups together — a
	// blank line breaks the current list (Wikidot doesn't fuse
	// lists across blank lines, and the paragraph wrapper would
	// otherwise wrap the gap in <p>).
	lines := strings.Split(text, "\n")
	var out strings.Builder
	i := 0
	for i < len(lines) {
		j := i
		// Find a contiguous run of list-able lines.
		for j < len(lines) {
			t := strings.TrimLeft(lines[j], " \t")
			if !(strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "# ")) {
				break
			}
			// But a blank line in the middle breaks the run.
			if strings.TrimSpace(lines[j]) == "" {
				break
			}
			j++
		}
		if j == i {
			out.WriteString(lines[i])
			if i < len(lines)-1 {
				out.WriteString("\n")
			}
			i++
			continue
		}
		out.WriteString(renderListBlock(lines[i:j]))
		if j < len(lines) {
			out.WriteString("\n")
		}
		i = j
	}
	return out.String()
}

// renderListBlock turns a slice of `* ` / `# ` lines (all part of
// one list region) into the corresponding nested HTML. We walk the
// lines with an explicit depth stack so the open/close order is
// always well-formed.
//
// Wikidot's nesting rules (mirrored here):
//   - 2 spaces of leading indent = 1 level of nesting depth.
//   - A child list sits INSIDE its parent's <li>: the parent's
//     <li> stays open until the child list is fully closed.
//   - Mixing `*` and `#` at the same level closes the current
//     list and opens a new one of the other type.
//
// Bug history: an earlier version of this function used the
// marker character itself ("*" / "#") as the HTML tag name,
// emitting `<*>` / `<#>` instead of `<ul>` / `<ol>`. That was
// visually plausible (browsers treat unknown tags as inline
// elements and don't break the layout) but rendered as
// content-less anchors and broke the DOM-based line-comment
// targeting. Fixed by routing every list open/close through
// listTagForMarker.
func renderListBlock(lines []string) string {
	type item struct {
		marker string // "*" or "#"
		indent int    // 0 = depth 1, 1 = depth 2, etc.
		body   string
	}
	items := make([]item, 0, len(lines))
	for _, line := range lines {
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		// Wikidot uses 2 spaces per indent level. Halve, rounding
		// down so a 1-space indent is still depth 1 (more
		// forgiving than the old "depth = leading/2 exactly").
		depth := leading / 2
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "* ") {
			items = append(items, item{"*", depth, t[2:]})
		} else if strings.HasPrefix(t, "# ") {
			items = append(items, item{"#", depth, t[2:]})
		} else {
			items = append(items, item{"", depth, t})
		}
	}

	var sb strings.Builder
	// openStack holds the marker for each currently-open list
	// (top = innermost). len(openStack) == current depth.
	openStack := make([]string, 0, 4)

	// emitLiOpen is called for every item to bring the open
	// <ul>/<ol> nesting to the right level for the upcoming
	// <li>. After this, the matching <li> has its parent list
	// open and any shallower lists closed. The caller still
	// has to decide whether to defer the </li> based on the
	// NEXT item's depth.
	emitLiOpen := func(it item) {
		target := it.indent + 1
		// Close levels that are deeper than we need now.
		for len(openStack) > target {
			sb.WriteString("</")
			sb.WriteString(listTagForMarker(openStack[len(openStack)-1]))
			sb.WriteString(">")
			openStack = openStack[:len(openStack)-1]
		}
		// Open levels until we hit the target depth.
		for len(openStack) < target {
			// Pick the marker: prefer the current item's
			// marker when we're opening fresh (no parent
			// list to inherit from), else fall back to the
			// last open marker so a continued `*` at the
			// same depth nests under the same <ul>.
			var m string
			if len(openStack) == 0 || (len(openStack) < it.indent) {
				if it.marker != "" {
					m = it.marker
				} else {
					m = "*"
				}
			} else {
				m = openStack[len(openStack)-1]
			}
			sb.WriteString("<")
			sb.WriteString(listTagForMarker(m))
			sb.WriteString(">")
			openStack = append(openStack, m)
		}
		// Same depth but marker differs → close & reopen
		// with the right marker (sibling list, not nested).
		if len(openStack) > 0 && it.marker != "" && openStack[len(openStack)-1] != it.marker {
			last := openStack[len(openStack)-1]
			sb.WriteString("</")
			sb.WriteString(listTagForMarker(last))
			sb.WriteString(">")
			openStack = openStack[:len(openStack)-1]
			sb.WriteString("<")
			sb.WriteString(listTagForMarker(it.marker))
			sb.WriteString(">")
			openStack = append(openStack, it.marker)
		}
	}

	for idx, it := range items {
		emitLiOpen(it)
		sb.WriteString("<li>")
		sb.WriteString(inlineOnly(it.body))

		// If the NEXT item is a deeper-nested child, leave
		// the <li> open so the child list nests inside it.
		// Otherwise close the <li> here.
		if idx == len(items)-1 {
			// last item; we'll close all open lists
			// after this iteration. Add a final </li>.
			sb.WriteString("</li>")
		} else {
			next := items[idx+1]
			if next.indent > it.indent {
				// child list coming — keep <li> open
				continue
			}
			sb.WriteString("</li>")
		}
	}
	// Close any remaining open lists.
	for len(openStack) > 0 {
		sb.WriteString("</")
		sb.WriteString(listTagForMarker(openStack[len(openStack)-1]))
		sb.WriteString(">")
		openStack = openStack[:len(openStack)-1]
	}
	return sb.String()
}

// listTagForMarker maps a wikidot list marker (`*` or `#`) to the
// corresponding HTML tag (`ul` or `ol`). A default of `ul` is used
// for the empty marker (defensive — the caller filters these out
// before invoking the open/close helpers, but a stray empty marker
// shouldn't break the output).
func listTagForMarker(m string) string {
	if m == "#" {
		return "ol"
	}
	return "ul"
}

// renderDivBlocks handles `[[div class="..."]]` and
// `[[div style="..."]]` blocks including arbitrary nesting. Regex
// non-greedy matching can't pair nested brackets correctly — the
// first `[[/div]]` always wins, leaving the rest as raw source.
// We use a manual byte-level scan that counts open/close tokens
// from each `[[div` until depth returns to zero.
//
// Recognised open tokens:
//   - `[[div class="..."]]`
//   - `[[div style="..."]]`
//   - `[[div data-<name>="..."]]` (Stage 4 addition —
//                                  data-* attributes are forwarded
//                                  verbatim so authors can hook
//                                  custom JS or framework-specific
//                                  attributes without escaping through
//                                  a generic `key="..."` form that
//                                  would risk CSS injection via the
//                                  `data-` namespace.)
//
// Close token: `[[/div]]`. Anything that looks like
// `[[div anything-else]]` is left alone (so future [[div class=…
// without quotes, or [[div id=…]], doesn't trip this parser).
func (p *wikidotParser) renderDivBlocks(source string) string {
	var sb strings.Builder
	i := 0
	for i < len(source) {
		// Find the next `[[div` opening. We restrict to the
		// two forms we support so `[[divider]]` or other
		// `[[div*]]` future syntax isn't accidentally
		// swallowed.
		idx := findNextDivOpen(source, i)
		if idx < 0 {
			sb.WriteString(source[i:])
			break
		}
		// Append everything before the match verbatim.
		sb.WriteString(source[i:idx])
		// Parse the open tag.
		kind, attrValue, contentStart, ok := parseDivOpen(source[idx:])
		if !ok {
			// Not a recognised form (shouldn't happen —
			// findNextDivOpen only returns positions of
			// recognised opens — but defensive). Skip one
			// character to avoid an infinite loop.
			sb.WriteString(source[idx : idx+1])
			i = idx + 1
			continue
		}
		// Walk forward from idx+contentStart, counting
		// open/close tokens until depth returns to 0. Both
		// contentStart and closeEnd/contentEnd are relative
		// to `idx` (i.e. relative to the start of the open
		// tag we just parsed).
		absStart := idx + contentStart
		closeEnd, contentEnd := walkDivBody(source, absStart)
		if closeEnd < 0 {
			// Unbalanced — leave the whole construct
			// intact so the author can see the broken
			// markup.
			sb.WriteString(source[idx:absStart])
			i = absStart
			continue
		}
		// Recursively convert the inner content (so nested
		// [[div …]] blocks, lists, code, etc. all get
		// processed). contentEnd is the absolute index of
		// the matching `[[/div]]` open-bracket; everything
		// from absStart up to that point is the inner body.
		// Use convertNoFootnote so a nested div block
		// doesn't append a duplicate footnote list to the
		// outer document.
		inner := p.convertNoFootnote(source[absStart:contentEnd])
		var out string
		switch kind {
		case "style":
			if css := sanitizeCSSValue(attrValue); css != "" {
				out = fmt.Sprintf(`<div style="%s">%s</div>`, css, inner)
			} else {
				out = inner
			}
		case "class":
			if cls := sanitizeAnchorID(attrValue); cls != "" {
				out = fmt.Sprintf(`<div class="%s">%s</div>`, cls, inner)
			} else {
				out = inner
			}
		case "data":
			// attrValue is the full attribute token including the
			// `data-*="..."` portion (the parser captures name + value
			// together so the data-attribute name is never detached
			// from its value). We HTML-escape the value but pass the
			// name through unchanged so authors can use any valid
			// `data-…` token (e.g. `data-toggle`, `data-id`, `data-spy`).
			out = fmt.Sprintf(`<div %s>%s</div>`, attrValue, inner)
		}
		sb.WriteString(p.storeBlock(out))
		i = closeEnd
	}
	return sb.String()
}

// findNextDivOpen returns the index of the next `[[div class="`,
// `[[div style="` or `[[div data-<name>="…"` open tag at or
// after `from`, or -1. The data-* form is matched by finding every
// `[[div data-` substring and taking the lowest index; the
// attribute name + value pair are then captured by parseDivOpen.
func findNextDivOpen(source string, from int) int {
	best := -1
	for _, prefix := range []string{"[[div class=\"", "[[div style=\"", "[[div data-"} {
		idx := strings.Index(source[from:], prefix)
		if idx < 0 {
			continue
		}
		abs := idx + from
		if best < 0 || abs < best {
			best = abs
		}
	}
	return best
}

// parseDivOpen inspects a div open tag at the start of `s` and
// returns:
//   - kind: "class" / "style" / "data"
//   - attrValue: the attribute value (the quoted string, or for
//                "data" the full `data-<name>="<value>"` token)
//   - contentStart: index (relative to s == source[idx:]) of
//     the first content character
//   - ok: true iff a valid open tag was found
func parseDivOpen(s string) (kind, attrValue string, contentStart int, ok bool) {
	if strings.HasPrefix(s, "[[div class=\"") {
		rest := s[len("[[div class=\""):]
		end := strings.Index(rest, "\"]]")
		if end < 0 {
			return "", "", 0, false
		}
		attr := rest[:end]
		return "class", attr, len("[[div class=\"") + end + 3, true
	}
	if strings.HasPrefix(s, "[[div style=\"") {
		rest := s[len("[[div style=\""):]
		end := strings.Index(rest, "\"]]")
		if end < 0 {
			return "", "", 0, false
		}
		attr := rest[:end]
		return "style", attr, len("[[div style=\"") + end + 3, true
	}
	// `[[div data-<name>="<value>"]]` form. The data- token
	// is treated as a single attribute; the name is matched
	// permissively (any non-] character other than the
	// closing `]]` block) because html5 data-* accepts
	// arbitrary names. The attribute name + value pair is
	// emitted verbatim — `sanitizeURLForAttr` would over-
	// restrict CSS content, so we route through
	// `sanitizeDataAttrValue` (a narrower allow-list) for
	// the value side only.
	if strings.HasPrefix(s, "[[div data-") {
		rest := s[len("[[div data-"):]
		// Read attribute name (`name="…"`)
		nameEnd := strings.Index(rest, "=\"")
		if nameEnd < 0 {
			return "", "", 0, false
		}
		name := rest[:nameEnd]
		// Reject if name has whitespace / non-token chars
		// (only letters / digits / dash / underscore).
		for _, c := range name {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-' || c == '_') {
				return "", "", 0, false
			}
		}
		// Find the closing `"]]` after the value's opening quote.
		valStart := len("[[div data-") + nameEnd + 2
		closeIdx := strings.Index(s[valStart:], "\"]]")
		if closeIdx < 0 {
			return "", "", 0, false
		}
		value := s[valStart : valStart+closeIdx]
		// The value is HTML-escaped so any `"` / `<` / `>`
		// in author-supplied text becomes an entity and
		// cannot break out of the wrapping attribute.
		// An empty (post-trim) value still produces a valid
		// `data-<name>=""` attribute so the author can
		// intentionally pass an empty data-* token.
		safe := strings.TrimSpace(value)
		if safe == "" {
			safe = ""
		} else {
			safe = html.EscapeString(safe)
		}
		fullAttr := fmt.Sprintf(`data-%s="%s"`, name, safe)
		return "data", fullAttr, valStart + closeIdx + 3, true
	}
	return "", "", 0, false
}

// walkDivBody returns (closeEnd, contentEnd) where contentEnd
// is the index of the first character of the matching `[[/div]]`
// and closeEnd is the index just past the matching close tag
// (i.e. after `]]`). closeEnd == -1 indicates the open tag was
// never closed — caller should leave the construct intact.
func walkDivBody(source string, contentStart int) (closeEnd, contentEnd int) {
	depth := 1
	i := contentStart
	for i < len(source) {
		// Find the next interesting token.
		nextOpen := findNextDivOpen(source, i)
		nextClose := strings.Index(source[i:], "[[/div]]")
		var nextTok int
		var isClose bool
		switch {
		case nextOpen < 0 && nextClose < 0:
			return -1, -1
		case nextOpen < 0:
			nextTok = i + nextClose
			isClose = true
		case nextClose < 0:
			nextTok = nextOpen
			isClose = false
		case nextOpen < i+nextClose:
			nextTok = nextOpen
			isClose = false
		default:
			nextTok = i + nextClose
			isClose = true
		}
		if isClose {
			depth--
			if depth == 0 {
				return nextTok + len("[[/div]]"), nextTok
			}
			i = nextTok + len("[[/div]]")
		} else {
			depth++
			// Skip past the open tag's attribute value.
			_, _, after, ok := parseDivOpen(source[nextTok:])
			if !ok {
				// Malformed — give up so we don't
				// loop forever.
				return -1, -1
			}
			i = nextTok + after
		}
	}
	return -1, -1
}

func wrapWikidotParagraphs(text string) string {
	// No local placeholder stash here — block-level HTML
	// (pre/table/div/ul/ol/blockquote/details/summary) is
	// already stashed via `p.storeBlock` in Phase 1 and
	// restored in Phase 10. A previous version of this
	// function ALSO stashed block HTML into a local
	// `%%WRAP_BLOCK_N%%` map, but that was redundant AND
	// actively wrong: the placeholders landed inside `<p>…</p>`
	// wrappers (the wrap pass treated them as inline text),
	// then the local restore expanded them back into the
	// middle of the `<p>`, producing invalid
	// `<p><div>…</div></p>` HTML. Trust the Phase 1 stash —
	// by the time this function runs in Phase 11, the only
	// block HTML still in the source is what came in via
	// the placeholder system, and our block-boundary check
	// below correctly skips those lines.

	// `%%BLOCK_N%%` / `%%WRAP_BLOCK_N%%` markers are placeholders
	// for block-level HTML that Phase 10 will restore. A line
	// that contains one in the middle (e.g. `inline prefix
	// %%BLOCK_5%% inline suffix`) needs to be split so the
	// marker gets its own line; otherwise paragraph-wrap would
	// emit `<p>… %%BLOCK_5%% …</p>` and the restored block
	// ends up inside the <p>, producing invalid HTML. The
	// regex `reBlockMarkerInLine` is declared at package level.

	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	var buf []string
	// blockInBuf detects whether a line in the buffer opens a
	// block-level element. If so, the whole buffer is emitted
	// verbatim (no <p> wrap) so we don't end up with invalid
	// `<p><div>…</div></p>` or `<p><table>…</table></p>` HTML.
	// Wikidot's block syntax ([[div …]], [[table …]], [[code]],
	// etc.) usually goes through a Phase-1 stash and is restored
	// as a placeholder, but a custom block written by the
	// author — or a Phase-2 inline that emits a block — can
	// sneak into the paragraph buffer. We also include `dl`
	// here so the renderWikidotDefList output (a contiguous
	// `<dl>…</dl>` block) doesn't end up with a stray
	// `<p><dl>…</dl></p>` wrap.
	blockInBuf := regexp.MustCompile(`<(?:div|table|pre|ul|ol|dl|h[1-6]|hr|blockquote|details|summary|section|aside)\b`)
	flush := func() {
		if len(buf) > 0 {
			joined := strings.Join(buf, "<br />\n")
			if blockInBuf.MatchString(joined) {
				// Don't wrap — emit the lines joined by
				// a soft break so the resulting DOM
				// matches the source layout.
				result = append(result, joined)
			} else {
				result = append(result, "<p>"+joined+"</p>")
			}
			buf = nil
		}
	}
	// blockTagStart is a "this line opens or closes a block"
	// detector. It's two regexes because the previous single
	// regex `^</?(…)\b` was a category error: `<div` matches
	// `<` then `d` — but the alternation expected `d` to be
	// the literal first character of a tag in the list
	// (`h[1-6]`, `hr`, `li`, …) and `d` isn't any of those, so
	// the whole match silently failed. Splitting the open
	// and close forms fixes the false-negative on `<div …>`
	// and `<pre …>` lines.
	blockOpenStart := regexp.MustCompile(`^<(h[1-6]|hr|li|p|img|blockquote|ul|ol|pre|table|div|details|summary|section|aside)\b`)
	blockCloseStart := regexp.MustCompile(`^</(h[1-6]|hr|li|p|img|blockquote|ul|ol|dl|pre|table|div|details|summary|section|aside)>`)
	// preOpen / preClose pair: when the wrap sees a `<pre>...</pre>`
	// block, it consumes every line from the opener through the
	// closer as a single opaque unit. The lines between are emitted
	// verbatim (no <br /> insertion, no <p> wrapping) so the
	// <pre>'d content keeps its original spacing. Without this
	// special-case, every newline inside `<pre><code>` ends up as
	// `<br />` and the whole body gets wrapped in `<p>`, producing
	// invalid `<p><pre><code>...<br /></code></pre></p>` HTML.
	preOpen := regexp.MustCompile(`(?i)^<pre\b`)
	preClose := regexp.MustCompile(`(?i)^</pre>`)
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		// If a `<pre>` line starts a block, accumulate
		// everything up to and including the matching
		// `</pre>` and emit it as a single chunk.
		// Subsequent lines (even ones that match
		// blockOpenStart themselves) get appended to
		// this chunk instead of being processed
		// individually.
		if preOpen.MatchString(trimmed) {
			flush()
			var preBlock []string
			preBlock = append(preBlock, line)
			// Single-line `<pre>…</pre>` case (rare but
			// possible if some upstream pass collapsed the
			// source): emit just that one line.
			if preClose.MatchString(trimmed) {
				result = append(result, preBlock...)
				continue
			}
			i++
			for i < len(lines) {
				preBlock = append(preBlock, lines[i])
				if preClose.MatchString(strings.TrimSpace(lines[i])) {
					break
				}
				i++
			}
			result = append(result, preBlock...)
			continue
		}
		// If a placeholder marker is buried in the middle of a
		// line (e.g. `inline prefix %%BLOCK_5%% inline suffix`),
		// split the line around it: the prefix joins the
		// paragraph buffer, the marker gets its own line (a
		// block boundary), and the suffix starts a fresh
		// paragraph buffer. Without this split the marker
		// would land inside a <p> and the restored block
		// would be wrapped in invalid `<p><div>…</div></p>`.
		if m := reBlockMarkerInLine.FindStringSubmatch(line); m != nil && (m[1] != "" || m[3] != "") {
			prefix := strings.TrimSpace(m[1])
			suffix := strings.TrimSpace(m[3])
			if prefix != "" {
				buf = append(buf, prefix)
			}
			flush()
			result = append(result, m[2])
			if suffix != "" {
				buf = append(buf, suffix)
			}
			continue
		}
		// Treat placeholder markers as block boundaries so a placeholder
		// that resolves to e.g. `<span>...</span>` or `<table>...</table>`
		// doesn't end up wrapped in a stray <p>. We also treat any
		// `[[__...]]` placeholder as a block boundary — those are the
		// Phase-1p TOC markers that the post-wrap Phase 12 will expand
		// into a real <div>. Without this check the TOC ends up
		// wrapped in <p>, which produces `<p><div>…</div></p>` (the
		// browser auto-closes the <p> at the <div>, leaving the TOC
		// inside an orphan paragraph fragment).
		if trimmed == "" || blockOpenStart.MatchString(trimmed) || blockCloseStart.MatchString(trimmed) ||
			strings.HasPrefix(trimmed, "%%WRAP_BLOCK_") ||
			strings.HasPrefix(trimmed, "%%BLOCK_") ||
			strings.HasPrefix(trimmed, "[[__") {
			flush()
			result = append(result, line)
		} else {
			buf = append(buf, trimmed)
		}
	}
	flush()

	return strings.Join(result, "\n")
}

// avoid unused import warnings in build configs that strip the slog
// reference; called from the renderers that surface module errors.
var _ = slog.Default

// ── Balanced `[[span class=...]]` replacement ────────────────────────
//
// Wikidot's span-class construct supports several semantic classes
// (ruby/rt/rb/keycap) that map to HTML5 elements, plus arbitrary
// class names that map to a generic `<span class>`. The grammar is
// balanced: a `[[span class="ruby"]]...[[/span]]` may contain a
// nested `[[span class="rt"]]...[[/span]]` whose close pairs with
// the *inner* `[[/span]]`.
//
// We can't match the deep balanced form with a single regex (Go's
// RE2 doesn't support recursion), but we can rely on a fixed-point
// strategy: match each *innermost* span (whose inner contains no
// `[[`) on every pass, map it to its target element, and loop until
// no change. Because each pass shrinks the source, the loop always
// terminates — and because the inner is empty-of-`[[`, the matched
// construct is provably innermost.
//
// reWDInnermostSpanClass captures one `[[span class="X"]]INNER[[/span]]`
// where INNER has no `[` — so the regex always picks the deepest
// match first. See renderBalancedSpanClass below for the loop.
var reWDInnermostSpanClass = regexp.MustCompile(`(?is)\[\[span class="([^"]*)"\]\]([^[]*?)\[\[/span\]\]`)

// renderBalancedSpanClass replaces balanced
// `[[span class="X"]]...[[/span]]` constructs in `s` with their
// HTML5 equivalents. See the doc-comment above the regex
// declaration for the loop strategy.
//
// Mapping:
//   - `class="keycap"`         → `<kbd>…</kbd>`
//   - `class="ruby"`           → `<ruby>…</ruby>`
//   - `class="rt"`             → `<rt>…</rt>`     (used nested inside ruby)
//   - `class="rb"`             → `<rb>…</rb>`     (used nested inside ruby)
//   - `class="foo bar"`        → `<span class="foo bar">…</span>`
//     (multi-class; each token must be `[A-Za-z0-9_-]+`)
//   - any class with an invalid token → wrapper dropped, inner kept verbatim
//   - empty class                       → wrapper dropped, inner kept verbatim
//
// This is the substitution function used by Phase 1k. We replaced
// the previous single-pass regex with this loop so the nested
// ruby/rt/rb case produces valid `<ruby>` HTML — the old code
// would emit a generic `<span class="ruby">…</span>` with the inner
// rt construct still inside the source text.
func renderBalancedSpanClass(s string) string {
	if !strings.Contains(s, `[[span class="`) {
		return s
	}
	prev := ""
	for prev != s {
		prev = s
		s = reWDInnermostSpanClass.ReplaceAllStringFunc(s, func(full string) string {
			m := reWDInnermostSpanClass.FindStringSubmatch(full)
			return mapSpanClassToElement(m[1], m[2])
		})
	}
	return s
}

// mapSpanClassToElement translates one balanced [[span class="X"]]pair[[/span]]
// to its HTML5 equivalent (or a generic `<span class>` for non-special
// classes). The HTML-escape for the inner content is the caller's job
// when the inner has user-controlled text; this helper preserves text
// verbatim so any pre-existing HTML (from a previous pass of the loop)
// survives the wrapping.
func mapSpanClassToElement(cls, inner string) string {
	switch strings.ToLower(strings.TrimSpace(cls)) {
	case "keycap":
		return `<kbd>` + inner + `</kbd>`
	case "ruby":
		return `<ruby>` + inner + `</ruby>`
	case "rt":
		return `<rt>` + inner + `</rt>`
	case "rb":
		return `<rb>` + inner + `</rb>`
	case "":
		return inner
	}
	// Generic class — sanitise each whitespace-separated token.
	// Any invalid token drops the wrapper entirely so a hostile
	// `class="x onclick=…"` can't inject attributes: a partially-
	// accepted multi-class would still be a footgun if the bad
	// token happened to be a valid HTML attribute name.
	parts := strings.Fields(cls)
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || !isValidClassName(p) {
			return inner
		}
		cleaned = append(cleaned, p)
	}
	if len(cleaned) == 0 {
		return inner
	}
	return `<span class="` + strings.Join(cleaned, " ") + `">` + inner + `</span>`
}

// isValidClassName returns true iff s consists only of
// [A-Za-z0-9_-]+ (i.e. a single CSS class identifier, no spaces,
// no escapes). The intent matches the previous
// `sanitizeAnchorID` rule but applied per-token so multi-class
// `class="foo bar"` strings survive the round-trip.
func isValidClassName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// applyWikidotInlineColor is the substitution function for
// reWDInlineColor. It's shared between the main Phase 2 inline
// pass (line ~1605) and the recursive inlineOnly() helper that
// other phases call back into for list items / table cells /
// span-class inner content — the two sites need to make the
// same decision about which colour form the user wrote.
//
// Recognised forms (matches `##<color>|<text>##`):
//   - `##<name>|text##`          — `name` is looked up in
//     the `colorNames` table; unknown names drop the wrapper.
//   - `##<#rgb>|text##`          — explicit hex with `#`.
//   - `##<rgb>|text##`           — bare hex without `#`, the form
//     the rule-wiki wikidot-syntax spec uses (e.g. `##44FF88|`).
//     We prepend `#` so CSS parses it correctly. Length must be
//     3/4/6/8 hex digits (CSS hex shorthand + rgba).
//
// Any non-conforming input drops the wrapper and emits the inner
// text verbatim — same fallback as a malformed class.
func applyWikidotInlineColor(s string) string {
	m := reWDInlineColor.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	name := strings.TrimSpace(m[1])
	text := m[2]

	// Hex (with or without `#`).
	hex := name
	if !strings.HasPrefix(hex, "#") && isPureHex(hex) {
		hex = "#" + hex
	}
	if strings.HasPrefix(hex, "#") {
		if css := sanitizeCSSValue(hex); css != "" {
			// CSS hex is 3, 4, 6, or 8 digits after the `#`.
			n := len(css) - 1
			if n == 3 || n == 4 || n == 6 || n == 8 {
				return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
			}
		}
		return text
	}

	// Named colour.
	if css, ok := colorNames[strings.ToLower(name)]; ok {
		return fmt.Sprintf(`<span style="color:%s">%s</span>`, css, html.EscapeString(text))
	}
	return text
}

// isPureHex reports true iff s is non-empty and contains only
// `[0-9A-Fa-f]`. Used by the inline-color handler to decide
// whether a colour value is a bare hex literal (the spec form)
// vs a CSS-named token.
func isPureHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
