// tests/e2e/revisions.cjs
// ────────────────────────────────────────────────────────────────────
// End-to-end Playwright smoke test for the article revision history
// feature (V1–V5).
//
//   1. Spin up a Chromium context (cookie jar managed by Playwright).
//   2. Log in as admin via /api/auth/csrf + /api/auth/login (parses
//      the math captcha via the same regex shape as the login form:
//      "(\d+)\s*([+\-×x*÷/])\s*(\d+)").
//   3. Create a fresh test article under a unique slug (so we don't
//      pollute any existing content).
//   4. PUT 5 content revisions via the API (faster than going through
//      the editor 5 times; the editor only needs to be touched at the
//      end to verify the UI).
//   5. Open the editor in the same context, click 📜 历史, screenshot
//      the drawer.
//   6. Click 回滚 on seq=2 → screenshot the confirm modal.
//   7. Confirm with a custom message → screenshot the updated editor.
//   8. Reload + reopen the drawer to capture the new top row.
//   9. Click 对比当前 on the restored row → screenshot the diff modal.
//  10. Tear down: delete the test article via the API.
//
// All API + page interactions share one Playwright context, so the
// session cookie set by /api/auth/login is automatically in scope for
// the page navigation that follows — no manual cookie plumbing.
//
// Pre-conditions:
//   - gokych backend running on :8000
//   - Next.js dev server running on :3000 (proxy /api/* to :8000)
//   - playwright installed at web/node_modules/playwright
//   - chromium installed under ~/Library/Caches/ms-playwright/
//   - admin / admin123 credentials (per .env)
//
// Run:
//   node tests/e2e/revisions.cjs
//
// Output:
//   /tmp/gokych-shots/revisions-{01..07}-*.png  (one per phase)
// ────────────────────────────────────────────────────────────────────

const { chromium } = require(
  require("path").resolve(__dirname, "..", "..", "web", "node_modules", "playwright")
);

const WEB = process.env.GOKYCH_WEB || "http://localhost:3000";
const SHOTS = "/tmp/gokych-shots";
const ADMIN_USER = "admin";
const ADMIN_PASS = "admin123";
const TYPE = "md";

// Captcha parser: dispatches on question shape.
//   - matrix mode (default for owners who toggled high-difficulty on):
//       [[a,b],[c,d]] × [[e,f],[g,h]] = ?
//     → reply with [[r1,r2],[r3,r4]] (compact JSON, no spaces inside).
//   - math mode (legacy default):
//       a + b / a - b / a × b / a ÷ b
//     → reply with the decimal result.
//
// The matrix regex is anchored on the literal "[[" prefix and "×" operator
// so it won't accidentally match numeric operands inside the brackets.
function solveCaptcha(question) {
  // matrix mode: 2×2 matrix multiplication
  const mm = question.match(
    /\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]\s*×\s*\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]/,
  );
  if (mm) {
    const A = [[+mm[1], +mm[2]], [+mm[3], +mm[4]]];
    const B = [[+mm[5], +mm[6]], [+mm[7], +mm[8]]];
    const R = [
      [
        A[0][0] * B[0][0] + A[0][1] * B[1][0],
        A[0][0] * B[0][1] + A[0][1] * B[1][1],
      ],
      [
        A[1][0] * B[0][0] + A[1][1] * B[1][0],
        A[1][0] * B[0][1] + A[1][1] * B[1][1],
      ],
    ];
    return `[[${R[0][0]},${R[0][1]}],[${R[1][0]},${R[1][1]}]]`;
  }
  // legacy math mode
  const m = question.match(/(\d+)\s*([+\-×x*÷/])\s*(\d+)/);
  if (!m) throw new Error(`unrecognised captcha: ${question}`);
  const [, a, op, b] = m;
  const x = parseInt(a, 10), y = parseInt(b, 10);
  let result;
  switch (op) {
    case "+": result = x + y; break;
    case "-": result = x - y; break;
    case "x": case "×": case "*": result = x * y; break;
    case "÷": case "/": result = x / y; break;
    default: throw new Error(`unknown op: ${op}`);
  }
  return String(result);
}

let browser;
let ctx;
let page;

// Module-level CSRF token, refreshed on every successful GET
// /api/auth/csrf. Mutating requests (POST/PUT/DELETE) attach it
// via the X-CSRF-Token header. The server's csrfMiddleware rejects
// any mutating request whose header doesn't match the token
// stored in the session.
let csrfToken = "";

// All API calls go through the page's own fetch (via page.evaluate).
// Going through the page, not ctx.request, has two benefits:
//   1. The fetch runs inside the browser's normal cookie store, so
//      the session + csrf cookies set by the login response are
//      guaranteed to ride on every subsequent request (no
//      cookie-jar timing surprises between ctx.request.fetch and
//      ctx.cookies()).
//   2. The browser's CSRF middleware checks against the *session*
//      cookie that THIS request carries — and the page's fetch
//      uses the same cookie document the next page.goto will use,
//      so we don't get drift between API walkthrough and UI walkthrough.
//
// The trade-off: each api() call round-trips through page.evaluate.
// For an e2e smoke test with 7 endpoints, the cost is ~10ms total.
async function api(method, url, body) {
  const fullUrl = `${WEB}${url}`;
  const result = await page.evaluate(
    async ({ method, fullUrl, body, csrfToken }) => {
      const init = {
        method,
        credentials: "include",
        headers: { "Content-Type": "application/json" },
      };
      if (body !== undefined && body !== null) {
        init.body = JSON.stringify(body);
      }
      if (method !== "GET" && csrfToken) {
        init.headers["X-CSRF-Token"] = csrfToken;
      }
      const res = await fetch(fullUrl, init);
      let parsed = null;
      try {
        parsed = await res.json();
      } catch {
        parsed = await res.text();
      }
      return { status: res.status, body: parsed };
    },
    { method, fullUrl, body, csrfToken },
  );
  if (result.status === 403) {
    console.error(`DBG api ${method} ${url} -> 403 body=${JSON.stringify(result.body)} csrfToken=${csrfToken.slice(0,16)}…`);
  }
  return result;
}

async function loginViaApi() {
  // 1) GET csrf + captcha (pre-login token)
  const csrfRes = await api("GET", "/api/auth/csrf");
  if (csrfRes.status !== 200) {
    throw new Error(`csrf GET failed: ${csrfRes.status} ${JSON.stringify(csrfRes.body)}`);
  }
  const captchaQuestion = csrfRes.body.captcha?.question;
  const preLoginToken = csrfRes.body.csrf_token;
  if (!captchaQuestion || !preLoginToken) {
    throw new Error(`bad csrf response: ${JSON.stringify(csrfRes.body)}`);
  }
  csrfToken = preLoginToken;
  // 2) POST login. NOTE: sessions.Login in internal/auth/session
  // wipes the entire session map and re-issues a fresh csrf_token
  // (defence against session fixation), so the token the server
  // expects on subsequent mutating requests is *different* from
  // the one we just used. We re-fetch it after login.
  const loginRes = await api("POST", "/api/auth/login", {
    username: ADMIN_USER,
    password: ADMIN_PASS,
    captcha: solveCaptcha(captchaQuestion),
    csrf_token: preLoginToken,
  });
  if (loginRes.status !== 200) {
    throw new Error(`login failed: ${loginRes.status} ${JSON.stringify(loginRes.body)}`);
  }
  // 3) Re-fetch csrf to get the post-login token.
  const postCsrf = await api("GET", "/api/auth/csrf");
  if (postCsrf.status !== 200) {
    throw new Error(`post-login csrf GET failed: ${postCsrf.status}`);
  }
  csrfToken = postCsrf.body.csrf_token;
  console.log(`✓ logged in (post-login csrf ${csrfToken.slice(0, 16)}…)`);
}

function buildEditorUrl(slug) {
  return `${WEB}/admin/articles/${TYPE}/${slug}`;
}

async function shot(name, caption) {
  const fs = require("fs");
  const path = require("path");
  fs.mkdirSync(SHOTS, { recursive: true });
  const p = path.join(SHOTS, `revisions-${name}.png`);
  await page.screenshot({ path: p, fullPage: true });
  console.log(`📸  ${p}\n    ${caption}`);
}

async function main() {
  const slug = `e2e-rev-${Date.now()}`;
  console.log(`▶ e2e-revisions: starting (slug=${slug})`);

  browser = await chromium.launch();
  ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  page = await ctx.newPage();
  page.on("pageerror", (err) => console.error("⚠ page error:", err.message));
  page.on("requestfailed", (req) =>
    console.error(`⚠ request failed: ${req.method()} ${req.url()} (${req.failure()?.errorText})`)
  );
  page.on("response", async (res) => {
    if (res.status() >= 400 && res.url().includes("/api/")) {
      console.error(`⚠ API ${res.status()}: ${res.request().method()} ${res.url()}`);
    }
  });

  // Phase A — log in
  // We must navigate to the origin before the page's fetch can use
  // the same-origin session cookie. A blank /api/auth/csrf from
  // about:blank won't carry any cookie. We just hit any same-origin
  // URL first (the login page itself works) so the browser context
  // has the right document origin; subsequent page.evaluate fetch
  // calls send cookies correctly.
  await page.goto(`${WEB}/auth/login`, { waitUntil: "domcontentloaded" });
  await loginViaApi();

  // Phase B — create a test article via the API
  const createRes = await api(
    "POST",
    `/api/articles?type=${TYPE}`,
    {
      slug,
      title: `E2E Revisions ${slug}`,
      content: "# e2e\n\ninitial content\n",
      tags: [],
    },
  );
  if (createRes.status !== 201) {
    throw new Error(`create article failed: ${createRes.status} ${JSON.stringify(createRes.body)}`);
  }
  console.log(`✓ created article: /${TYPE}/${slug}`);

  // Phase C — 5 sequential PUTs (each becomes a new revision row)
  const contents = [
    "v1 content\n",
    "v1 content\nv2 content\n",
    "v1 content\nv2 content\nv3 content\n",
    "v1 content\nv2 content\nv3 content\nv4 content\n",
    "v1 content\nv2 content\nv3 content\nv4 content\nv5 content\n",
  ];
  for (let i = 0; i < contents.length; i++) {
    const r = await api(
      "PUT",
      `/api/articles/${TYPE}/${slug}`,
      {
        title: `E2E Revisions ${slug} (v${i + 1})`,
        content: contents[i],
        tags: [],
        message: `e2e step ${i + 1}`,
      },
    );
    if (r.status !== 200) {
      throw new Error(`PUT #${i + 1} failed: ${r.status} ${JSON.stringify(r.body)}`);
    }
  }
  console.log(`✓ wrote 5 revisions (seq 2..6)`);

  // Phase D — drive the UI in the same context
  await page.goto(buildEditorUrl(slug), { waitUntil: "networkidle" });
  await shot("01-editor", "Article editor after 5 saves");

  // Open 📜 历史
  await page.click('[data-testid="open-revisions"]');
  await page.waitForSelector('[data-testid="revision-drawer"].open', { timeout: 5000 });
  // wait briefly + diagnose if rows don't appear
  try {
    await page.waitForSelector('[data-testid^="revision-row-"]', { timeout: 5000 });
  } catch (e) {
    const body = await page.evaluate(() => {
      const drawer = document.querySelector(".side-drawer-body");
      return drawer ? drawer.innerText : "(no drawer body)";
    });
    console.error(`DBG drawer body after click: ${body.slice(0, 500)}`);
    // also probe directly
    const probe = await page.evaluate(async () => {
      const r = await fetch("/api/articles/md/" + window.location.pathname.split("/").pop() + "/revisions?page=1&per_page=100", { credentials: "include" });
      return { status: r.status, text: (await r.text()).slice(0, 200) };
    });
    console.error(`DBG direct fetch:`, probe);
    throw e;
  }
  await shot("02-drawer-list", "Revision drawer — 6 rows (1 snapshot + 5 diffs)");

  // Click 回滚 on row seq=2
  await page.click('[data-testid="revision-restore-2"]');
  await page.waitForSelector('[data-testid="restore-confirm"]', { timeout: 5000 });
  await shot("03-restore-modal-empty", "Restore confirm modal — empty message");

  // Fill message + confirm
  await page.fill('[data-testid="restore-message-input"]', "e2e: roll back to v1");
  await shot("04-restore-modal-filled", "Restore confirm modal — with message");

  await page.click('[data-testid="restore-confirm"]');
  await page.waitForTimeout(800);
  await shot("05-editor-after-restore", "Editor after restore — should now show v1 content");

  // Reload the page so the revision list re-fetches and shows the
  // new seq=7 row at the top.
  await page.reload({ waitUntil: "networkidle" });
  // The drawer may still be open after reload (state preserved) or
  // closed — try clicking either way. If already open, the button
  // toggles it closed, so re-click to guarantee open.
  await page.click('[data-testid="open-revisions"]').catch(() => {});
  // Diagnostic: dump drawer body if seq=7 doesn't appear within 5s.
  try {
    await page.waitForSelector('[data-testid="revision-row-7"]', { timeout: 5000 });
  } catch (e) {
    const dbg = await page.evaluate(() => {
      const drawer = document.querySelector(".side-drawer-body");
      const isOpen = document.querySelector(".side-drawer.open");
      return {
        drawerOpen: !!isOpen,
        bodyText: drawer ? drawer.innerText.slice(0, 500) : "(no drawer)",
        seqs: Array.from(document.querySelectorAll('[data-testid^="revision-row-"]'))
          .map(el => el.getAttribute("data-testid")),
      };
    });
    console.error("DBG after reload+click:", JSON.stringify(dbg, null, 2));
    await page.screenshot({ path: "/tmp/gokych-shots/revisions-FAIL-after-restore.png", fullPage: true });
    throw e;
  }
  await shot("06-drawer-after-restore", "Drawer after restore — top row is the new seq=7");

  // Click 对比当前 on row seq=6 — pre-restore latest. After restoring
  // to v1, current is also v1, so comparing #2 ↔ current would be
  // empty (the diff modal shows a "no diff" notice instead of the
  // `revision-diff-content` element). #6 ↔ current is non-trivial: #6
  // had v5 content, current is v1, so the diff body renders.
  await page.click('[data-testid="revision-compare-6"]');
  await page.waitForSelector('[data-testid="revision-diff-content"]', { timeout: 5000 });
  await shot("07-diff-modal", "Diff modal — #6 (v5) ↔ #7 (current, v1)");

  // Tear down — delete the test article while the browser is still
  // alive (the session cookie + csrf token live in the page's fetch
  // context; closing the browser first leaves the api() helper
  // dangling and DELETE throws "Target page ... has been closed").
  const delRes = await api("DELETE", `/api/articles/${TYPE}/${slug}`);
  if (delRes.status !== 200) {
    console.error(`⚠ delete failed: ${delRes.status} ${JSON.stringify(delRes.body)}`);
  } else {
    console.log(`✓ deleted test article`);
  }
  await browser.close();
  browser = null;
  console.log("✓ e2e-revisions: all phases complete");
}

main().catch(async (e) => {
  console.error("✗ e2e-revisions:", e.message);
  if (browser) await browser.close();
  process.exit(1);
});
