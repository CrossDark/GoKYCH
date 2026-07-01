/**
 * Client-side Wikidot parser - mirrors the Go backend parser
 * for live preview in the editor without hitting the server.
 *
 * Core syntax is supported. Server-dependent features (include,
 * module ListPages, toc, user mentions, %%vars%%) show placeholder
 * text since they require database context.
 */

// Size keyword map
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

// Color name map
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
  for (const ch of raw) {
    if (ch === '"' || ch === "'" || ch === '<' || ch === '>' || ch === '`' || (ch < ' ' && ch !== '\t') || ch === '\x7f') {
      return "";
    }
  }
  if (raw.startsWith("#")) return raw;
  if (raw.startsWith("/")) {
    if (raw.length > 1 && (raw[1] === "/" || raw[1] === "\\")) return "";
    return raw;
  }
  try {
    const u = new URL(raw);
    if (["http:", "https:", "mailto:"].includes(u.protocol)) return raw;
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
      ch === "_" || ch === "#" || ch === "." || ch === "," ||
      ch === "%" || ch === "/" || ch === "-" || ch === " " || ch === ":";
    if (!ok) return "";
  }
  return raw;
}

// Apply inline formatting to text (bold, italic, underline, strike, monospace, color, etc.)
function applyInline(text: string): string {
  let out = text;

  // Protect code/raw blocks first - they shouldn't have inline formatting applied
  const protectedBlocks: string[] = [];
  // Inline code: {{...}}
  out = out.replace(/\{\{([\s\S]*?)\}\}/g, (_m, code) => {
    const idx = protectedBlocks.length;
    protectedBlocks.push(`<code>${escapeHtml(code)}</code>`);
    return `\x00PROT${idx}\x00`;
  });
  // Raw/literal: @@...@@
  out = out.replace(/@@([\s\S]*?)@@/g, (_m, raw) => {
    const idx = protectedBlocks.length;
    protectedBlocks.push(`<span class="wikidot-raw">${escapeHtml(raw)}</span>`);
    return `\x00PROT${idx}\x00`;
  });

  // Escape HTML in the remaining text
  out = escapeHtml(out);

  // Now apply inline formatting
  // Bold: **text**
  out = out.replace(/\*\*([^\*\n]+?)\*\*/g, "<strong>$1</strong>");
  // Italic: //text//
  out = out.replace(/\/\/([^\/\n]+?)\/\//g, "<em>$1</em>");
  // Underline: __text__
  out = out.replace(/__([^_\n]+?)__/g, "<u>$1</u>");
  // Strikethrough: --text--
  out = out.replace(/--([^-\n]+?)--/g, "<s>$1</s>");
  // Superscript: ^^text^^
  out = out.replace(/\^\^([^\^\n]+?)\^\^/g, "<sup>$1</sup>");
  // Subscript: ,,text,,
  out = out.replace(/,,([^,\n]+?),,/g, "<sub>$1</sub>");

  // Inline color: ##color|text##
  out = out.replace(/##([^#|\n]+)\|([^#\n]+)##/g, (_m, color, content) => {
    let css = color.toLowerCase().trim();
    if (colorNames[css]) css = colorNames[css];
    else css = sanitizeCssValue(color);
    if (css) return `<span style="color:${css}">${content}</span>`;
    return content;
  });

  // Inline links: [url text] or [url|text] or [[[url|text]]]
  // Single bracket: [http://example.com text]
  out = out.replace(/\[([^\s\[\]]+)(?:\s+([^\]]+))?\]/g, (_m, url, text) => {
    const safe = sanitizeUrl(url);
    if (safe) {
      const label = text || safe;
      return `<a href="${safe}" target="_blank" rel="noopener noreferrer">${label}</a>`;
    }
    return _m;
  });
  // Triple bracket: [[[page|text]]] or [[[page]]]
  out = out.replace(/\[\[\[([^\[\]|]+)(?:\|([^\[\]]+))?\]\]\]/g, (_m, target, text) => {
    const label = text || target;
    // Internal page link - just show as link placeholder
    return `<a href="/${escapeHtml(target.trim())}" class="wikidot-internal-link">${label.trim()}</a>`;
  });

  // Image: [[image url]] or [[image url attribute="value"]]
  out = out.replace(/\[\[image\s+([^\s\]]+)(?:[^\]]*)\]\]/gi, (_m, url) => {
    const safe = sanitizeUrl(url);
    if (safe) return `<img src="${safe}" alt="" loading="lazy" style="max-width:100%">`;
    return "";
  });

  // Inline size/color/font/bg tags: [[size ...]]...[[/size]] etc.
  // These are block-level in some cases but handle simple inline use here
  out = out.replace(/\[\[size\s+([^\]]+)\]\]([\s\S]*?)\[\[\/size\]\]/gi, (_m, size, content) => {
    let css = size.toLowerCase().trim();
    if (sizeMap[css]) css = sizeMap[css];
    else {
      // Handle px values
      if (/^\d+(\.\d+)?px$/i.test(size)) css = size;
      else css = sanitizeCssValue(size);
    }
    if (css) return `<span style="font-size:${css}">${content}</span>`;
    return content;
  });
  out = out.replace(/\[\[color\s+([^\]]+)\]\]([\s\S]*?)\[\[\/color\]\]/gi, (_m, color, content) => {
    let css = color.toLowerCase().trim();
    if (colorNames[css]) css = colorNames[css];
    else css = sanitizeCssValue(color);
    if (css) return `<span style="color:${css}">${content}</span>`;
    return content;
  });
  out = out.replace(/\[\[font\s+([^\]]+)\]\]([\s\S]*?)\[\[\/font\]\]/gi, (_m, font, content) => {
    const css = sanitizeCssValue(font);
    if (css) return `<span style="font-family:${css}">${content}</span>`;
    return content;
  });

  // Restore protected blocks
  out = out.replace(/\x00PROT(\d+)\x00/g, (_m, idx) => protectedBlocks[parseInt(idx)] || "");

  return out;
}

// Process a single block-level element (paragraph, heading, list item, etc.)
function processBlock(line: string): string {
  line = line.trim();
  if (!line) return "";

  // Headings: + Title, ++ Subtitle, etc.
  const headingMatch = line.match(/^(\++)\s+(.*)$/);
  if (headingMatch) {
    const level = Math.min(headingMatch[1].length, 6);
    const content = applyInline(headingMatch[2]);
    return `<h${level}>${content}</h${level}>`;
  }

  // Horizontal rule: ---- (4+ dashes)
  if (/^-{4,}$/.test(line)) {
    return "<hr>";
  }

  // Blockquote: > text or [[div style="..."]] style quotes
  if (line.startsWith("> ")) {
    const content = applyInline(line.slice(2));
    return `<blockquote>${content}</blockquote>`;
  }

  // Alignment: [[= centered]]  [[< left]]  [[> right]]
  const alignMatch = line.match(/^\[\[([=<>])\s+(.*?)\]\]$/);
  if (alignMatch) {
    const align = alignMatch[1] === "=" ? "center" : alignMatch[1] === "<" ? "left" : "right";
    const content = applyInline(alignMatch[2]);
    return `<div style="text-align:${align}">${content}</div>`;
  }

  // Math block: $$...$$ (single line form)
  if (line.startsWith("$$") && line.endsWith("$$") && line.length > 4) {
    return `<div class="math-block">${escapeHtml(line.slice(2, -2))}</div>`;
  }

  // Regular paragraph
  return `<p>${applyInline(line)}</p>`;
}

// Process table rows
function processTable(lines: string[]): string {
  let html = '<div class="wikidot-table-wrapper"><table class="wikidot-table">';
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    // Parse cells: ||~header||cell|| or ||cell||cell||
    const cells: string[] = [];
    const parts = trimmed.split("||");
    for (const part of parts) {
      const cell = part.trim();
      if (cell === "") continue;
      if (cell.startsWith("~")) {
        cells.push(`<th>${applyInline(cell.slice(1).trim())}</th>`);
      } else {
        cells.push(`<td>${applyInline(cell)}</td>`);
      }
    }
    if (cells.length > 0) {
      html += "<tr>" + cells.join("") + "</tr>";
    }
  }
  html += "</table></div>";
  return html;
}

// Process lists (bulleted * and numbered #)
function processList(lines: string[]): string {
  // Determine list type and build nested structure
  let html = "";
  let currentTag = "";
  let items: string[] = [];

  for (const line of lines) {
    const match = line.match(/^([*#]+)\s+(.*)$/);
    if (!match) continue;
    const markers = match[1];
    const content = applyInline(match[2]);
    const tag = markers[markers.length - 1] === "*" ? "ul" : "ol";

    if (tag !== currentTag && items.length > 0) {
      html += `<${currentTag} class="wikidot-list">${items.join("")}</${currentTag}>`;
      items = [];
    }
    currentTag = tag;
    items.push(`<li>${content}</li>`);
  }
  if (items.length > 0) {
    html += `<${currentTag} class="wikidot-list">${items.join("")}</${currentTag}>`;
  }
  return html;
}

export function renderWikidot(source: string): string {
  if (!source) return "";

  // Protect code blocks first
  const codeBlocks: string[] = [];
  let working = source.replace(/\[\[code(?:\s+type="?(\w+)"?)?\]\]([\s\S]*?)\[\[\/code\]\]/gi, (_m, lang, code) => {
    const idx = codeBlocks.length;
    const langClass = lang ? ` class="language-${lang}"` : "";
    codeBlocks.push(`<pre><code${langClass}>${escapeHtml(code.trim())}</code></pre>`);
    return `\x00CODE${idx}\x00`;
  });

  // Protect collapsible blocks
  const collapsibleBlocks: string[] = [];
  working = working.replace(/\[\[collapsible(?:\s+show="?([^"\]]+)"?)?(?:\s+hide="?([^"\]]+)"?)?\]\]([\s\S]*?)\[\[\/collapsible\]\]/gi, (_m, show, _hide, content) => {
    const idx = collapsibleBlocks.length;
    const label = show || "展开";
    // Process inner content recursively
    const innerHtml = renderWikidot(content);
    collapsibleBlocks.push(`<details class="wikidot-collapsible"><summary>${escapeHtml(label)}</summary><div class="collapsible-content">${innerHtml}</div></details>`);
    return `\x00COLLAPSE${idx}\x00`;
  });

  // Handle server-dependent features with placeholder notices
  const placeholderBlocks: string[] = [];
  working = working.replace(/\[\[(?:include|module|toc|user|\*user)\s+[^\]]*\]\](?:[\s\S]*?\[\[\/(?:module)\]\])?/gi, (m) => {
    const idx = placeholderBlocks.length;
    const name = m.match(/\[\[(\w+)/)?.[1] || "block";
    placeholderBlocks.push(`<div class="wikidot-placeholder"><em>[${name} 需要服务器端预览]</em></div>`);
    return `\x00PLACEHOLDER${idx}\x00`;
  });

  // %%var%% substitutions - show placeholder
  working = working.replace(/%%([^%]+)%%/g, (_m, name) => {
    return `<code class="wikidot-var">%%${escapeHtml(name)}%%</code>`;
  });

  // Split into paragraphs by blank lines
  const paragraphs = working.split(/\n\s*\n/);
  const output: string[] = [];

  let i = 0;
  while (i < paragraphs.length) {
    const para = paragraphs[i];

    // Check for tables (lines starting with ||)
    if (/^\s*\|\|/.test(para)) {
      const tableLines = para.split("\n");
      output.push(processTable(tableLines));
      i++;
      continue;
    }

    // Check for lists (lines starting with * or #)
    if (/^\s*[*#]/.test(para)) {
      const listLines = para.split("\n").filter(l => /^\s*[*#]/.test(l));
      output.push(processList(listLines));
      i++;
      continue;
    }

    // Process each line in the paragraph
    const lines = para.split("\n");
    let paraHtml = "";
    for (const line of lines) {
      // Check for [[div]] / [[span]] blocks
      const divMatch = line.match(/^\[\[div(?:\s+style="?([^"\]]+)"?)?\]\]$/i);
      const spanMatch = line.match(/^\[\[span(?:\s+style="?([^"\]]+)"?)?\]\]$/i);
      const closeDiv = /^\[\[\/div\]\]$/i.test(line.trim());
      const closeSpan = /^\[\[\/span\]\]$/i.test(line.trim());

      if (divMatch) {
        const style = divMatch[1] ? ` style="${sanitizeCssValue(divMatch[1])}"` : "";
        paraHtml += `<div${style}>`;
      } else if (spanMatch) {
        const style = spanMatch[1] ? ` style="${sanitizeCssValue(spanMatch[1])}"` : "";
        paraHtml += `<span${style}>`;
      } else if (closeDiv) {
        paraHtml += "</div>";
      } else if (closeSpan) {
        paraHtml += "</span>";
      } else {
        const blockHtml = processBlock(line);
        if (blockHtml) paraHtml += blockHtml;
      }
    }
    if (paraHtml) output.push(paraHtml);
    i++;
  }

  let result = output.join("\n");

  // Restore code blocks
  result = result.replace(/\x00CODE(\d+)\x00/g, (_m, idx) => codeBlocks[parseInt(idx)] || "");
  // Restore collapsible blocks
  result = result.replace(/\x00COLLAPSE(\d+)\x00/g, (_m, idx) => collapsibleBlocks[parseInt(idx)] || "");
  // Restore placeholders
  result = result.replace(/\x00PLACEHOLDER(\d+)\x00/g, (_m, idx) => placeholderBlocks[parseInt(idx)] || "");

  // Handle [[br]] → <br> (Wikidot explicit line break)
  result = result.replace(/\[\[br\]\]/gi, "<br>");

  // Handle inline math $...$ (after HTML escape)
  result = result.replace(/\$([^\$\n]+?)\$/g, (_m, expr) => {
    return `<span class="math-inline">${escapeHtml(expr)}</span>`;
  });

  return result;
}
