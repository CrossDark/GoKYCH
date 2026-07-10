#!/usr/bin/env python3
"""Integration smoke test for the bug fixes (tasks 1/2/5/6 completion).

Drives the live backend on :8111. Requires GIN_MODE=debug so the session
cookie isn't flagged Secure (an HTTP client won't resend a Secure cookie
over plain HTTP, which would make every CSRF check fail).
"""
import json, re, sys, urllib.request, urllib.error
from http.cookiejar import CookieJar

BASE = "http://localhost:8111/api"
PASS = 0; FAIL = 0
def check(name, cond, detail=""):
    global PASS, FAIL
    if cond: PASS += 1; print(f"  \u2713 {name}")
    else: FAIL += 1; print(f"  \u2717 {name}  {detail}")

opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
# bare opener has NO cookie jar — used to isolate the X-API-Key path so a
# bogus/valid key is tested without a session cookie authenticating the
# request behind our back.
bare = urllib.request.build_opener()
def _call(op, path, method, headers, body, decode=True):
    h = dict(headers or {})
    data = None
    if body is not None:
        data = json.dumps(body).encode(); h["Content-Type"] = "application/json"
    r = urllib.request.Request(BASE+path, data=data, method=method, headers=h)
    try:
        resp = op.open(r); raw = resp.read()
        return resp.status, (raw.decode(errors="replace") if decode else raw)
    except urllib.error.HTTPError as e:
        raw = e.read()
        return e.code, (raw.decode(errors="replace") if decode else raw)
def req(path, method="GET", headers=None, body=None):
    return _call(opener, path, method, headers, body)
def req_bare(path, method="GET", headers=None, body=None):
    return _call(bare, path, method, headers, body)
def req_bytes(path, method="GET", headers=None, body=None):
    return _call(opener, path, method, headers, body, decode=False)

def solve(q):
    # matrix mode (default for owners who enabled high-difficulty):
    #   [[a,b],[c,d]] × [[e,f],[g,h]] = ?
    # answer is the 2×2 product as compact JSON.
    m = re.match(
        r"\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]\s*×\s*"
        r"\[\[(-?\d+),(-?\d+)\],\[(-?\d+),(-?\d+)\]\]", q)
    if m:
        A = [[int(m.group(1)), int(m.group(2))],
             [int(m.group(3)), int(m.group(4))]]
        B = [[int(m.group(5)), int(m.group(6))],
             [int(m.group(7)), int(m.group(8))]]
        R = [[A[0][0]*B[0][0] + A[0][1]*B[1][0],
              A[0][0]*B[0][1] + A[0][1]*B[1][1]],
             [A[1][0]*B[0][0] + A[1][1]*B[1][0],
              A[1][0]*B[0][1] + A[1][1]*B[1][1]]]
        return f"[[{R[0][0]},{R[0][1]}],[{R[1][0]},{R[1][1]}]]"
    # legacy math mode: a + b / a - b / a × b
    m = re.match(r"(\d+)\s*([+\-×])\s*(\d+)", q)
    if not m: return ""
    a, op, b = int(m.group(1)), m.group(2), int(m.group(3))
    return str({"+": a+b, "-": a-b, "×": a*b}[op])

# 1. csrf + captcha
st, body = req("/auth/csrf")
j = json.loads(body); csrf = j["csrf_token"]; ans = solve(j["captcha"]["question"])
check("csrf+captcha fetched", st==200 and csrf and ans, f"status={st} q={j['captcha']['question']}")

# 2. owner login
st, body = req("/auth/login","POST",{"X-CSRF-Token":csrf},
               {"username":"admin","password":"admin123","captcha":ans,"csrf_token":csrf})
login_ok = st==200 and json.loads(body).get("status")=="ok"
check("owner password login", login_ok, f"status={st} body={body[:140]}")

st, body = req("/auth/me")
me = json.loads(body)["user"] if st==200 else None
check("logged in as owner", me and me.get("role")=="owner", f"me={me}")

# Login rotates the CSRF token (session-fixation protection wipes+reissues
# it), so fetch a fresh one before any mutating call.
st, body = req("/auth/csrf")
csrf = json.loads(body)["csrf_token"]

# ── Bug A: owner can access api-keys ──
st, body = req("/admin/api-keys","GET",{"X-CSRF-Token":csrf})
check("owner GET /admin/api-keys 200", st==200, f"status={st}")

st, body = req("/admin/api-keys","POST",{"X-CSRF-Token":csrf},{"name":"integration-test"})
created = json.loads(body) if st==201 else {}
key_id = created.get("id"); plain = created.get("plaintext_key")
check("owner POST /admin/api-keys (create)", st==201 and bool(plain), f"status={st} body={body[:140]}")

# ── Bug B: api key authenticates + CSRF skipped on mutations ──
# Use the cookie-less opener so the key is the ONLY credential presented.
st, body = req_bare("/auth/me","GET",{"X-API-Key":plain})
km = json.loads(body).get("user") if st==200 else None
check("X-API-Key GET /auth/me authenticates owner", st==200 and km and km["id"]==me["id"], f"status={st} body={body[:120]}")

st, body = req_bare("/admin/notifications","POST",{"X-API-Key":plain},
               {"title":"apikey-test","content":"via key","is_important":False,"is_active":True})
mut_ok = st in (200,201)
check("Bug B: api key POST mutation (CSRF skipped)", mut_ok, f"status={st} body={body[:180]}")
if mut_ok:
    nid = json.loads(body).get("id")
    if nid: req_bare(f"/admin/notifications/{nid}","DELETE",{"X-API-Key":plain})

# cookie path WITHOUT csrf still blocked (session cookie present, no token)
st, body = req("/admin/notifications","POST",None,{"title":"x","content":"x","is_important":False,"is_active":False})
check("cookie path WITHOUT csrf token -> 403", st==403, f"status={st}")

# bogus key does not auth (cookie-less so the session can't rescue it)
st, body = req_bare("/auth/me","GET",{"X-API-Key":"gky_"+"0"*64})
check("bogus X-API-Key does not authenticate", st==200 and json.loads(body).get("user") is None, f"status={st} body={body[:120]}")

# ── Bug F: PDF compile + cache (PDF-first, no prior HTML view) ──
# NB: run this BEFORE the reguser login below — that login overwrites the
# owner session in the shared cookie jar, and the PDF test needs admin
# rights to create the typst article.
import time, subprocess
PDFSLUG = "pdf-cache-test"
req(f"/articles/typst/{PDFSLUG}","DELETE",{"X-CSRF-Token":csrf})
st, body = req("/articles?type=typst","POST",{"X-CSRF-Token":csrf},
    {"slug":PDFSLUG,"title":"PDF Cache Test",
     "content":"#set page(width: auto, height: auto)\nHello PDF cache test."})
check("Bug F: create typst article", st==201, f"status={st} body={body[:120]}")
t0 = time.time()
st, pdf1 = req_bytes(f"/articles/typst/{PDFSLUG}/pdf","GET")
d1 = time.time()-t0
check("Bug F: first PDF compile returns 200 + PDF bytes", st==200 and pdf1[:4]==b"%PDF", f"status={st} bytes={len(pdf1)} d={d1:.2f}s")
t0 = time.time()
st, pdf2 = req_bytes(f"/articles/typst/{PDFSLUG}/pdf","GET")
d2 = time.time()-t0
check("Bug F: second PDF is cache hit (identical, much faster)", st==200 and pdf1==pdf2 and d2 < max(0.05, d1*0.3), f"d1={d1:.2f}s d2={d2:.3f}s same={pdf1==pdf2}")
q = f"SELECT COALESCE(LENGTH(pdf_content),0) FROM typst_cache c JOIN articles a ON a.id=c.article_id WHERE a.type='typst' AND a.slug='{PDFSLUG}'"
plen = subprocess.run(["mysql","-ugokych","-pgokych","-hlocalhost","-sN","-e",q,"gokych"], capture_output=True, text=True).stdout.strip()
check("Bug F: typst_cache.pdf_content populated (caches PDF-first)", plen and int(plen)>0, f"pdf_len={plen}")
req(f"/articles/typst/{PDFSLUG}","DELETE",{"X-CSRF-Token":csrf})

# ── Bug A: non-owner gets 403 ──
req("/admin/users","POST",{"X-CSRF-Token":csrf},{"username":"reguser","password":"User12345","role":"user"})
st, body = req("/auth/csrf"); j=json.loads(body); c2=j["csrf_token"]; a2=solve(j["captcha"]["question"])
st, body = req("/auth/login","POST",{"X-CSRF-Token":c2},{"username":"reguser","password":"User12345","captcha":a2,"csrf_token":c2})
reg_ok = st==200 and json.loads(body).get("status")=="ok"
# need a CSRF token for reguser's OWN session for the api-keys call (requireOwner checks role, csrf also enforced)
st, body = req("/auth/csrf"); c3=json.loads(body)["csrf_token"]
if reg_ok:
    st, body = req("/admin/api-keys","GET",{"X-CSRF-Token":c3})
    check("Bug A: non-owner GET /admin/api-keys -> 403", st==403, f"status={st} body={body[:120]}")
else:
    check("Bug A: non-owner GET /admin/api-keys -> 403", False, f"reguser login failed status={st} {body[:120]}")

# ── Bug D: passkey begin/finish ──
# fresh csrf for owner session
st, body = req("/auth/csrf"); csrf2=json.loads(body)["csrf_token"]
st, body = req("/auth/passkey/login/begin","POST",{"X-CSRF-Token":csrf2})
check("passkey login/begin 200 + challenge", st==200 and "challenge" in body.lower(), f"status={st} body={body[:160]}")

st, body = req("/auth/passkey/login/finish","POST",{"X-CSRF-Token":csrf2},
               {"credential":{"id":"x","rawId":"AAAA","type":"public-key","response":{}}})
check("passkey login/finish rejects bad cred as 4xx (no body crash)", st in (400,401,403), f"status={st} body={body[:160]}")

# ── Task 5: PDF ──
st, body = req("/articles/md/nope/pdf")
check("PDF non-typst -> 404", st==404, f"status={st}")
st, body = req("/articles/typst/nope-slug/pdf")
check("PDF missing typst -> 404", st==404, f"status={st}")

# cleanup
if key_id: req(f"/admin/api-keys/{key_id}","DELETE",{"X-CSRF-Token":csrf})

print(f"\n=== {PASS} passed, {FAIL} failed ===")
sys.exit(1 if FAIL else 0)