package parsers

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gokych/internal/typst"
)

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
	// reWDGenericAttr matches a single `key="value"` pair, shared by
	// the generic [[div ...]]/[[span ...]] attribute parser.
	reWDGenericAttr = regexp.MustCompile(`([a-zA-Z][\w-]*)\s*=\s*"([^"]*)"`)
	reWDTable       = regexp.MustCompile(`(?is)\[\[table\]\](.*?)\[\[/table\]\]`)
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
	reWDFormOpen      = regexp.MustCompile(`(?is)\[\[form((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDFormClose     = regexp.MustCompile(`\[\[/form\]\]`)
	reWDInput         = regexp.MustCompile(`(?is)\[\[input((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDTextareaOpen  = regexp.MustCompile(`(?is)\[\[textarea((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDTextareaClose = regexp.MustCompile(`\[\[/textarea\]\]`)
	// NOTE: `reWDButton` (the legacy wikidot button used at line
	// ~487 for `[[button Label|target]]`) keeps that name. Our
	// form-widget button — `[[button attrs]]` with arbitrary
	// attributes but no `|` separator — is named `reWDFormButton`
	// so the two syntaxes stay distinct: a `[[button label|…]]`
	// passes the legacy regex, a `[[button type="…" …]]` passes
	// this one. The order in which the two are tried determines
	// which one wins.
	reWDFormButton  = regexp.MustCompile(`(?is)\[\[button((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDCheckbox    = regexp.MustCompile(`(?is)\[\[checkbox((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDRadio       = regexp.MustCompile(`(?is)\[\[radio((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDSelectOpen  = regexp.MustCompile(`(?is)\[\[select((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDSelectClose = regexp.MustCompile(`\[\[/select\]\]`)
	reWDOptionOpen  = regexp.MustCompile(`(?is)\[\[option((?:\s+[a-zA-Z][\w-]*(\s*=\s*"[^"]*")?)*)\s*\]\]`)
	reWDOptionClose = regexp.MustCompile(`\[\[/option\]\]`)

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
	// reWDLiteral — `@@...@@` is the Wikidot literal-escape
	// construct. Earlier revisions used `@@([^@]+?)@@` which
	// happily matched across line and block boundaries: a stray
	// `@@` in body prose (e.g. the spec-source description line
	// "...用两个@@包围它") would consume every line — including
	// intervening `[[code]]` blocks and paragraphs — until it
	// found the next `@@` two paragraphs down. That destroyed
	// the blockquote, def-list and `[!-- ...]` sections in
	// between. Restrict the inner capture to a single line so
	// the regex only matches an actual literal escape that's
	// fully written on one source line.
	reWDLiteral = regexp.MustCompile(`@@([^@\n]+?)@@`)
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

	// Blockquote marker — require at least ONE space after the
	// leading `>` chain. Without that constraint, `>>嵌套引用<<`
	// (the French reverse guillemet pair, with no whitespace) gets
	// mis-consumed as a 2-level blockquote; the actual
	// typographic transformation happens later in the smart-punct
	// pass. require at least one ASCII space (or tab) so the
	// blockquote-marker interpretation is exclusive.
	reWDBlockquote    = regexp.MustCompile(`(?m)^(>+[ \t])(.*)$`)
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
	// reWDBackslashContinuation matches `X\\\nY` (backslash at end of line).
	// Wikidot supports this as a line continuation: the backslash and
	// newline are stripped, joining the two lines (used for wrapping
	// long paragraphs in source without introducing <br> breaks).
	reWDBackslashContinuation = regexp.MustCompile(`\\\r?\n`)

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

	// Typst is the shared typst worker used by the typst renderer to look
	// up cached compiled HTML. May be nil — in that case the typst branch
	// renders a "compile pending" placeholder instead of forking the CLI
	// (matching the previous package-level-db fallback behaviour, but
	// without the global mutable state).
	Typst *typst.Worker

	// Ctx is the request context for context-aware DB/typst operations.
	// May be nil (falls back to context.Background() internally).
	Ctx context.Context
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

	// ── Phase 0.55: stash [[code]] blocks early ───────────────────
	// `@@...@@` is a literal escape, but it lives next to
	// `[[code]]...[[/code]]` in source — and Wikidot
	// semantics say both should be verbatim. Running
	// `@@...@@` BEFORE the code stash would let a stray
	// `@@` inside a `[[code]]` block get consumed. We
	// therefore stash code blocks FIRST as opaque
	// placeholders, run the literal pass on the remainder,
	// then re-stash the code placeholders into the parser
	// block map under their original key so Phase 10
	// restores them exactly once.
	codeBlocks := []string{}
	out = reWDCode.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDCode.FindStringSubmatch(s)
		codeBlocks = append(codeBlocks, renderCodeBlock(m[2], m[1]))
		// Use a marker that contains `@` so the literal
		// regex cannot accidentally match across it.
		return fmt.Sprintf("\x00CODEBLOCK_%d\x00", len(codeBlocks)-1)
	})

	// ── Phase 0.6: literal escape @@...@@ ─────────────────────────
	// Runs AFTER %%var%% (so vars are still substituted
	// outside `@@...@@`) and AFTER the code-block stash
	// (so a `@@` inside a `[[code]]` block is preserved
	// verbatim). The block-stash pass at Phase 1c will
	// later no-op on these placeholders, but we restore
	// the stashed code-block HTML directly below.
	out = reWDLiteral.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDLiteral.FindStringSubmatch(s)
		// HTML-escape the inner text BEFORE stashing so the
		// restored block is safe to drop into a paragraph
		// without re-running the entity-encoding pass.
		return p.storeBlock(html.EscapeString(m[1]))
	})

	// Restore code-block placeholders to real block markers
	// so Phase 10 finds them as separate stash entries (one
	// per code block, in source order).
	for i, htmlBlock := range codeBlocks {
		out = strings.ReplaceAll(out, fmt.Sprintf("\x00CODEBLOCK_%d\x00", i), p.storeBlock(htmlBlock))
	}

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

	// 1d.5b Backslash line continuation: end-of-line `\`
	// joins the next line directly (no space inserted),
	// per Wikidot spec (used inside blockquotes and
	// long paragraphs for source wrapping).
	out = reWDBackslashContinuation.ReplaceAllString(out, "")

	// Smart-punct moved to AFTER Phase 8 (blockquote rendering) so a
	// line-leading `>` / `>>` / `<<` is consumed as a blockquote
	// marker before the smart-punct pass sees it. Otherwise the
	// `reWDSmartRAQuote` rule (`>>` → `»`) eats the `>>` of a nested
	// blockquote and renders it as `<p>» 嵌套</p>`.
	//
	// The 1d.7 phase used to live here; moved to right-after-Phase 8
	// below so the typographic transformations (`...` → `…`,
	// `--` → `—`, `` `` '' '' `` → curly quotes, etc.) only run on
	// real prose, never on quote structure.

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

	// 1k. [[span class=…]] / [[span style=…]] / [[span key="v" …]] —
	// inline only, no block patterns or paragraph wrapping (span is
	// inline; nesting <p> inside it is invalid HTML).
	//
	// Uses a balanced fixed-point matcher (renderBalancedSpans below)
	// so nested `[[span class="ruby"]]...[[span class="rt"]]...
	// [[/span]][[/span]]` constructs produce valid HTML5 `<ruby>` /
	// `<rt>` elements, and so [[span class="x" style="y"]] with
	// multiple attributes is handled in one pass.
	out = renderBalancedSpans(out)

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
	// Add `wikidot-align` class so the front-end line-number
	// allocator can skip the wrapper div itself (the inner
	// <p> tags produced by Phase 11 are the real content lines).
	out = reWDCenter.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:center">$1</div>`)
	out = reWDLeftBlock.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:left">$1</div>`)
	out = reWDRight.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:right">$1</div>`)
	out = reWDJustify.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:justify">$1</div>`)

	// 1m.5 Single-line alignment shortcuts. Runs after the
	// block forms so a `[[<]]` opener on the same line as
	// the inline shortcut can't be confused for one. The
	// regex matches `=` / `<` followed by a space at the
	// START of a line, so inline mentions of `=` (e.g.
	// `x = y`) are not promoted into alignment divs.
	out = reWDCenterLine.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:center">$1</div>`)
	out = reWDLeftLine.ReplaceAllString(out, `<div class="wikidot-align" style="text-align:left">$1</div>`)

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
		return processWikidotLink("*"+url, text)
	})

	// 3c. Internal wiki link `[[[page]]]` / `[[[page|alias]]]`.
	// Run AFTER the starred-triple form (3c.5) so a `*[[...]]`
	// marker is consumed by the new-window branch first.
	out = reWDWikiLink.ReplaceAllStringFunc(out, func(s string) string {
		m := reWDWikiLink.FindStringSubmatch(s)
		target := strings.TrimSpace(m[1])
		alias := strings.TrimSpace(m[2])
		return processWikidotLink(target, alias)
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

	// ── Phase 8.5: smart punctuation ─────────────────────────────────
	//
	// Was Phase 1d.7 (before block stashes); now runs AFTER
	// renderWikidotBlockquotes so a line-leading `>>` is consumed as
	// a nested blockquote marker (`>> text` → `<blockquote><blockquote>…`),
	// not as French guillemet close (`>>` → `»`).
	//
	// Em-dash uses a non-greedy whitespace check so we
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

	// ── Phase 10b: render inline formatting inside heading text ──────
	//
	// Heading text captured in Phase 4 (`p.headings[].Text`) is the
	// RAW wikidot source at the time the heading regex matched —
	// BEFORE Phase 2 (inline formatting) and Phase 3 (links/images)
	// ran on the main `out` buffer. Phase 10 above restored block
	// placeholders (`%%BLOCK_N%%` → stored HTML) inside the captured
	// Text, but inline wikidot markup like `//italic//`, `**bold**`,
	// `[[[link]]]`, `[[span …]]`, etc. is still raw source. If we
	// feed that straight to the TOC builder (Phase 12) the markup
	// gets html.EscapeString'd and shows up as literal text in the
	// table of contents.
	//
	// Run the inline-only renderer on each heading's Text so the TOC
	// sees proper HTML. We use renderWikidotHeadingInline which is a
	// TOC/heading-safe subset of inlineOnly + balanced spans + size,
	// deliberately skipping block constructs (no <p>, no <div>, no
	// heading-inside-heading).
	for i := range p.headings {
		p.headings[i].Text = renderWikidotHeadingInline(p.headings[i].Text)
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
		row = strings.ReplaceAll(row, "%%rating%%", "请使用页面内置评分")
		row = strings.ReplaceAll(row, "%%rating_count%%", "请使用页面内置评分")
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
		// Wikidot's convention is to treat H1 as the article
		// title (it's a body-level landing-page convention, the
		// H1 should not be repeated in the toc). But that default
		// breaks down when an article has NO H2/H3 — the toc then
		// shows nothing even though every H1 is a real section
		// heading. rule-wiki's [[toc]] for a spec page is exactly
		// that case (every entry is `+ Heading`).
		//
		// Compute the lowest level present and clamp minTocLevel
		// down to it so the toc never ends up empty when there
		// are headings to show.
		effectiveMin := 2
		lowest := 7
		for _, h := range p.headings {
			if h.SkipTOC {
				continue
			}
			if h.Level < lowest {
				lowest = h.Level
			}
		}
		if lowest < effectiveMin {
			effectiveMin = lowest
		}
		if effectiveMin < 1 {
			effectiveMin = 1
		}
		inner = renderWikidotTOCList(p.headings, effectiveMin)
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
// (`**bold****, `//italic//“, `[link]`) survives, but
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
		case "multi":
			// attrValue is the full rendered attribute string
			// (e.g. `class="x" style="y" data-foo="z"`).
			out = fmt.Sprintf(`<div %s>%s</div>`, attrValue, inner)
		}
		sb.WriteString(p.storeBlock(out))
		i = closeEnd
	}
	return sb.String()
}

// findNextDivOpen returns the index of the next `[[div ` open tag
// (followed by whitespace and attributes) at or after `from`, or -1.
// The `[[divider]]` form is excluded because it has no space after `div`;
// parseDivOpen validates that the tag is well-formed.
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
