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

  // Style tags
  out = out.replace(/\[size=([^\]]+)\]([\s\S]*?)\[\/size\]/gi, (_match, size, text) => {
    let css = size.toLowerCase();
    if (sizeMap[css]) {
      css = sizeMap[css];
    } else {
      css = sanitizeCssValue(size);
      if (!css) return text;
    }
    return `<span style="font-size:${css}">${text}</span>`;
  });
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
