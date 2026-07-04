/**
 * Client-side BBCode parser - mirrors the Go backend parser
 * for live preview in the editor without hitting the server.
 */

// Size keyword map (matches Go backend sizeMap)
const sizeMap: Record<string, string> = {
  "xx-small": "0.5rem",
  "x-small": "0.625rem",
  smaller: "0.75rem",
  small: "0.8rem",
  medium: "1rem",
  large: "1.25rem",
  "x-large": "1.5rem",
  "xx-large": "2rem",
  larger: "2.5rem",
};

// Maps the classic HTML 1-7 font scale (`[size=N]` for N in 1..7)
// to rem values, so `[size=7]` renders huge instead of an invalid
// unitless `font-size:7`.
const sizeScaleMap: Record<string, string> = {
  "1": "0.5rem",
  "2": "0.75rem",
  "3": "1rem",
  "4": "1.25rem",
  "5": "1.5rem",
  "6": "1.75rem",
  "7": "2rem",
};

// resolveBBCodeSize turns a `[size=X]` token into a valid CSS
// font-size. Mirrors the Go backend's resolveBBCodeSize so the live
// preview in the admin editor matches what readers actually see.
//
// Accepted forms:
//   1. keyword in sizeMap (xx-small, large, medium, …) → rem
//   2. plain integer 1-7 → HTML font scale (sizeScaleMap, rem)
//   3. plain number >7: ≤40 → Npx (e.g. `[size=14]` → 14px);
//      >40 → N% (e.g. `[size=150]` → 150%, the phpBB convention)
//   4. numeric + explicit CSS unit (`12pt`, `0.8em`, `24px`,
//      `1.25rem`, `80%`) → as-is
//
// Returns "" when the value can't be made safe — caller drops the
// wrapper rather than emitting invalid CSS like `font-size:150`.
// Anything outside the four shapes (e.g. `[size=giant]`, a made-up
// keyword) returns "" so it doesn't slip through sanitizeCssValue's
// loose alphanumerics-only filter and render as `font-size:giant`.
const SIZE_EXPLICIT_UNIT = /^\d+(?:\.\d+)?(?:px|pt|em|rem|%)$/i;
function resolveBBCodeSize(token: string): string {
  const trimmed = token.trim();
  if (!trimmed) return "";
  const keyword = sizeMap[trimmed.toLowerCase()];
  if (keyword) return keyword;
  // Bare number (int or decimal like `12.5`).
  if (/^\d+(\.\d+)?$/.test(trimmed)) {
    if (sizeScaleMap[trimmed]) return sizeScaleMap[trimmed];
    const n = parseFloat(trimmed);
    if (n > 40) return trimmed + "%";
    return trimmed + "px";
  }
  // Numeric + explicit unit. Whitelist match gates the value before
  // we hand it to sanitizeCssValue so made-up keywords can't slip
  // through alphanumerics-only filtering.
  if (SIZE_EXPLICIT_UNIT.test(trimmed)) {
    const css = sanitizeCssValue(trimmed);
    if (css) return css;
  }
  return "";
}

// extractBBCodeSizeBody scans `text` from `from` (just past a
// `[size=…]` opener) for the matching `[/size]`, accounting for
// nested `[size=…]` openers. Returns the inner body + ok flag + the
// index immediately after the close tag. ok=false means no close.
function extractBBCodeSizeBody(
  text: string,
  from: number
): { body: string; ok: boolean; next: number } {
  const close = "[/size]";
  let j = from;
  let depth = 1;
  const openRe = /\[size=[^\]]+\]/gi;
  while (j < text.length) {
    openRe.lastIndex = j;
    const openMatch = openRe.exec(text);
    const openStart = openMatch ? openMatch.index : -1;
    const openEnd = openMatch ? openStart + openMatch[0].length : -1;
    const closeAt = text.indexOf(close, j);
    if (closeAt < 0) return { body: "", ok: false, next: 0 };
    if (openStart >= 0 && openStart < closeAt) {
      depth++;
      j = openEnd;
      continue;
    }
    depth--;
    const closeEnd = closeAt + close.length;
    if (depth === 0) {
      return { body: text.slice(from, closeAt), ok: true, next: closeEnd };
    }
    j = closeEnd;
  }
  return { body: "", ok: false, next: 0 };
}

// renderBBCodeSizeBlocks mirrors the Go backend's
// renderBBCodeSizeBlocks: depth-counted walk over `[size=…]…[/size]`
// so nested size blocks become nested `<span style="font-size:…">`
// wrappers. Unknown size values (typo or sanitiser-rejected CSS
// injection) drop the wrapper and re-emit only the inner body, the
// same as Wikidot's `[[size bad]]x[[/size]]` → `x` fallback.
function renderBBCodeSizeBlocks(text: string): string {
  let out = "";
  let i = 0;
  const openRe = /\[size=[^\]]+\]/gi;
  while (i < text.length) {
    openRe.lastIndex = i;
    const m = openRe.exec(text);
    if (!m) {
      out += text.slice(i);
      return out;
    }
    // Emit prefix verbatim.
    out += text.slice(i, m.index);
    const openerEnd = m.index + m[0].length;
    const css = resolveBBCodeSize(m[0].slice("[size=".length, -1));
    const { body, ok, next } = extractBBCodeSizeBody(text, openerEnd);
    if (!ok) {
      // No matching close — leave the opener raw and bail.
      out += text.slice(m.index);
      return out;
    }
    if (css) {
      out += `<span style="font-size:${css}">${renderBBCodeSizeBlocks(body)}</span>`;
    } else {
      // Bad/unknown value — drop the wrapper, re-emit only the body.
      out += renderBBCodeSizeBlocks(body);
    }
    i = next;
  }
  return out;
}

// Color name map (matches Go backend colorNames)
const colorNames: Record<string, string> = {
  red: "#e74c3c",
  green: "#27ae60",
  blue: "#3498db",
  yellow: "#f1c40f",
  orange: "#e67e22",
  purple: "#9b59b6",
  pink: "#e91e63",
  gray: "#7f8c8d",
  grey: "#7f8c8d",
  black: "#2c3e50",
  white: "#ecf0f1",
  cyan: "#00bcd4",
  teal: "#009688",
  indigo: "#3f51b5",
};

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}

function sanitizeUrl(raw: string): string {
  raw = raw.trim();
  if (!raw) return "";
  // Reject attribute-breaking characters
  for (const ch of raw) {
    if (ch === '"' || ch === "'" || ch === '<' || ch === '>' || ch === '`' || (ch < ' ' && ch !== '\t') || ch === '\x7f') {
      return "";
    }
  }
  // Fragment only
  if (raw.startsWith("#")) {
    return raw;
  }
  // Same-site path
  if (raw.startsWith("/")) {
    if (raw.length > 1 && (raw[1] === "/" || raw[1] === "\\")) return "";
    return raw;
  }
  // Check scheme
  try {
    const u = new URL(raw);
    if (["http:", "https:", "mailto:"].includes(u.protocol)) {
      return raw;
    }
  } catch {
    return "";
  }
  return "";
}

function sanitizeCssValue(raw: string): string {
  raw = raw.trim();
  if (!raw) return "";
  const sanitised = raw.replace(/;/g, " ");
  const lower = sanitised.toLowerCase();
  for (const bad of ["{", "}", "(", ")", "expression", "javascript:", "url(", "@import"]) {
    if (lower.includes(bad)) return "";
  }
  for (const ch of sanitised) {
    const ok =
      (ch >= "a" && ch <= "z") ||
      (ch >= "A" && ch <= "Z") ||
      (ch >= "0" && ch <= "9") ||
      ch === "_" ||
      ch === "#" ||
      ch === "." ||
      ch === "," ||
      ch === "%" ||
      ch === "/" ||
      ch === "-" ||
      ch === " " ||
      ch === ":";
    if (!ok) return "";
  }
  return raw;
}

function sanitizeAnchorId(raw: string): string {
  raw = raw.trim();
  if (!raw) return "";
  for (const ch of raw) {
    const ok =
      (ch >= "a" && ch <= "z") ||
      (ch >= "A" && ch <= "Z") ||
      (ch >= "0" && ch <= "9") ||
      ch === "_" ||
      ch === "-";
    if (!ok) return "";
  }
  return raw;
}

function renderCode(code: string, lang: string): string {
  const c = code.trim();
  if (lang) {
    return `<pre><code class="language-${escapeHtml(lang)}">${c}</code></pre>`;
  }
  return `<pre><code>${c}</code></pre>`;
}

function renderQuote(text: string, author: string): string {
  const t = text.trim();
  const cite = author ? `<cite>${author}</cite>` : "";
  return `<blockquote class="bbcode-quote">${cite}${t}</blockquote>`;
}

function renderSpoiler(text: string, title: string): string {
  const t = text.trim();
  const label = title || "Spoiler";
  return `<details class="bbcode-spoiler"><summary>${label}</summary><div class="bbcode-spoiler-content">${t}</div></details>`;
}

function renderTable(content: string): string {
  const rows = content.split("[/tr]");
  let html = '<div class="bbcode-table-wrapper"><table class="bbcode-table">';
  for (const row of rows) {
    const r = row.trim();
    if (!r) continue;
    html += "<tr>";
    const cellRe = /\[(td|th)\]([\s\S]*?)\[\/\1\]/gi;
    let m;
    while ((m = cellRe.exec(r)) !== null) {
      html += `<${m[1]}>${m[2]}</${m[1]}>`;
    }
    html += "</tr>";
  }
  html += "</table></div>";
  return html;
}

function parseLists(text: string): string {
  const listRe = /\[list(?:=(1))?\]([\s\S]*?)\[\/list\]/gi;
  let result = text;
  let iterations = 0;
  while (listRe.test(result) && iterations < 10) {
    iterations++;
    result = result.replace(listRe, (_match, ordered, inner) => {
      const tag = ordered === "1" ? "ol" : "ul";
      // Split items
      let parts = inner.split("[/*]");
      if (parts.length <= 1) {
        parts = inner.split("[*]");
      }
      const items: string[] = [];
      for (let p of parts) {
        p = p.trim();
        p = p.replace(/^\[\*\]/, "").trim();
        if (p) items.push(`<li>${p}</li>`);
      }
      return `<${tag} class="bbcode-list">${items.join("")}</${tag}>`;
    });
  }
  return result;
}

export function renderBBCode(source: string): string {
  if (!source) return "";
  let out = escapeHtml(source);

  // Block-level first
  // Code blocks - protect from further processing
  const codePlaceholders: string[] = [];
  out = out.replace(/\[code(?:=(\w+))?\]([\s\S]*?)\[\/code\]/gi, (_match, lang, code) => {
    const idx = codePlaceholders.length;
    codePlaceholders.push(renderCode(code, lang || ""));
    return `\x00CODE${idx}\x00`;
  });

  out = out.replace(/\[quote(?:=([\s\S]*?))?\]([\s\S]*?)\[\/quote\]/gi, (_match, author, text) =>
    renderQuote(text, author || "")
  );
  out = out.replace(/\[spoiler(?:=([\s\S]*?))?\]([\s\S]*?)\[\/spoiler\]/gi, (_match, title, text) =>
    renderSpoiler(text, title || "")
  );

  out = parseLists(out);

  out = out.replace(/\[table\]([\s\S]*?)\[\/table\]/gi, (_match, content) => renderTable(content));
  out = out.replace(/\[hr\]/gi, "<hr>");
  out = out.replace(/\[center\]([\s\S]*?)\[\/center\]/gi, '<div style="text-align:center">$1</div>');
  out = out.replace(/\[right\]([\s\S]*?)\[\/right\]/gi, '<div style="text-align:right">$1</div>');
  out = out.replace(/\[left\]([\s\S]*?)\[\/left\]/gi, '<div style="text-align:left">$1</div>');

  // Headings (block-level). Inline formatting inside is still processed
  // by the inline passes below, so `[h1][b]x[/b][/h1]` →
  // <h1><strong>x</strong></h1>. Done before `[size=…]` so a heading
  // that contains a `[size=14]` wrapper keeps the heading as the
  // outer tag (otherwise the span would wrap part of the heading
  // text). Matches the Go backend's heading pass.
  out = out.replace(/\[h1\]([\s\S]*?)\[\/h1\]/gi, "<h1>$1</h1>");
  out = out.replace(/\[h2\]([\s\S]*?)\[\/h2\]/gi, "<h2>$1</h2>");
  out = out.replace(/\[h3\]([\s\S]*?)\[\/h3\]/gi, "<h3>$1</h3>");
  out = out.replace(/\[h4\]([\s\S]*?)\[\/h4\]/gi, "<h4>$1</h4>");
  out = out.replace(/\[h5\]([\s\S]*?)\[\/h5\]/gi, "<h5>$1</h5>");
  out = out.replace(/\[h6\]([\s\S]*?)\[\/h6\]/gi, "<h6>$1</h6>");

  // Inline
  out = out.replace(/\[b\]([\s\S]*?)\[\/b\]/gi, "<strong>$1</strong>");
  out = out.replace(/\[i\]([\s\S]*?)\[\/i\]/gi, "<em>$1</em>");
  out = out.replace(/\[u\]([\s\S]*?)\[\/u\]/gi, "<u>$1</u>");
  out = out.replace(/\[s\]([\s\S]*?)\[\/s\]/gi, "<s>$1</s>");
  out = out.replace(/\[sup\]([\s\S]*?)\[\/sup\]/gi, "<sup>$1</sup>");
  out = out.replace(/\[sub\]([\s\S]*?)\[\/sub\]/gi, "<sub>$1</sub>");

  // URLs
  out = out.replace(/\[url=([^\]]+)\]([\s\S]*?)\[\/url\]/gi, (_match, url, text) => {
    const safe = sanitizeUrl(url);
    if (safe) {
      return `<a href="${safe}" target="_blank" rel="noopener noreferrer">${text}</a>`;
    }
    return text;
  });
  out = out.replace(/\[url\]([\s\S]*?)\[\/url\]/gi, (_match, url) => {
    const safe = sanitizeUrl(url);
    if (safe) {
      return `<a href="${safe}" target="_blank" rel="noopener noreferrer">${safe}</a>`;
    }
    return "";
  });
  out = out.replace(/\[email\]([\s\S]*?)\[\/email\]/gi, (_match, email) => {
    const safe = sanitizeUrl("mailto:" + email);
    if (safe) {
      return `<a href="${safe}">${email}</a>`;
    }
    return email;
  });
  out = out.replace(/\[img\]([\s\S]*?)\[\/img\]/gi, (_match, url) => {
    const safe = sanitizeUrl(url);
    if (safe) {
      return `<img src="${safe}" alt="" loading="lazy" style="max-width:100%">`;
    }
    return "";
  });

  // Style tags — `[size=…]` has a richer grammar than `[color]`/
  // `[font]`/`[bg]`: beyond the keyword table we accept `1`-`7`
  // (HTML font scale, rem) and a bare number with implicit `px`
  // (≤40) or `%` (>40, phpBB convention). resolveBBCodeSize encodes
  // that table; falling back to it makes `[size=14]` render at 14px
  // and `[size=150]` at 150% instead of producing unitless
  // `font-size:14` / `font-size:150` (which the browser silently
  // ignores — the whole reason this branch used to look "broken").
  // Depth-counted walker handles nested `[size=…]` correctly.
  out = renderBBCodeSizeBlocks(out);
  out = out.replace(/\[color=([^\]]+)\]([\s\S]*?)\[\/color\]/gi, (_match, color, text) => {
    let css = color.toLowerCase();
    if (colorNames[css]) {
      css = colorNames[css];
    } else {
      css = sanitizeCssValue(color);
      if (!css) return text;
    }
    return `<span style="color:${css}">${text}</span>`;
  });
  out = out.replace(/\[font=([^\]]+)\]([\s\S]*?)\[\/font\]/gi, (_match, font, text) => {
    const css = sanitizeCssValue(font);
    if (css) return `<span style="font-family:${css}">${text}</span>`;
    return text;
  });
  out = out.replace(/\[bg=([^\]]+)\]([\s\S]*?)\[\/bg\]/gi, (_match, bg, text) => {
    const css = sanitizeCssValue(bg);
    if (css) return `<span style="background-color:${css}">${text}</span>`;
    return text;
  });

  out = out.replace(/\[video\]([\s\S]*?)\[\/video\]/gi, (_match, url) => {
    const safe = sanitizeUrl(url);
    if (safe) return `<video controls style="max-width:100%"><source src="${safe}"></video>`;
    return "";
  });
  out = out.replace(/\[audio\]([\s\S]*?)\[\/audio\]/gi, (_match, url) => {
    const safe = sanitizeUrl(url);
    if (safe) return `<audio controls><source src="${safe}"></audio>`;
    return "";
  });
  out = out.replace(/\[anchor\]([\s\S]*?)\[\/anchor\]/gi, (_match, id) => {
    const safe = sanitizeAnchorId(id);
    if (safe) return `<span id="${safe}" class="bbcode-anchor"></span>`;
    return "";
  });

  // Restore code blocks
  out = out.replace(/\x00CODE(\d+)\x00/g, (_match, idx) => codePlaceholders[parseInt(idx)] || "");

  // Unescape escaped brackets
  out = out.replace(/\\\[/g, "[").replace(/\\\]/g, "]");

  return out;
}
