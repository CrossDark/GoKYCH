#!/usr/bin/env python3
"""Integration test for the profile-centred passkey/password redesign.

Uses an independent cookie jar per login session so one test's logout
never invalidates another's CSRF token.
"""
import json, re, sys, urllib.request, urllib.error
from http.cookiejar import CookieJar

BASE = "http://localhost:8111/api"
PASS = 0; FAIL = 0
def check(name, cond, detail=""):
    global PASS, FAIL
    if cond: PASS += 1; print(f"  \u2713 {name}")
    else: FAIL += 1; print(f"  \u2717 {name}  {detail}")

def solve(q):
    # matrix mode: [[a,b],[c,d]] × [[e,f],[g,h]] = ?
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
    # legacy math mode
    m = re.match(r"(\d+)\s*([+\-×])\s*(\d+)", q)
    if not m: return ""
    a, op, b = int(m.group(1)), m.group(2), int(m.group(3))
    return str({"+": a+b, "-": a-b, "×": a*b}[op])

class Sess:
    """One logged-in session with its own cookie jar + live csrf token."""
    def __init__(self):
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        self.csrf = None
    def req(self, path, method="GET", body=None):
        h = {}; data = None
        if body is not None: data = json.dumps(body).encode(); h["Content-Type"] = "application/json"
        if self.csrf: h["X-CSRF-Token"] = self.csrf
        r = urllib.request.Request(BASE+path, data=data, method=method, headers=h)
        try:
            resp = self.op.open(r); return resp.status, resp.read().decode()
        except urllib.error.HTTPError as e:
            return e.code, e.read().decode()
    def csrf_get(self):
        st, body = self.req("/auth/csrf")
        self.csrf = json.loads(body)["csrf_token"]
        return json.loads(body)

def login(user, pw):
    s = Sess()
    j = s.csrf_get()
    ans = solve(j["captcha"]["question"])
    st, body = s.req("/auth/login","POST",{"username":user,"password":pw,"captcha":ans,"csrf_token":s.csrf})
    # login rotates csrf → fetch a fresh token before any mutation
    s.csrf_get()
    return st, body, s

# ── ensure test users exist (owner session) ──
st, body, owner = login("admin","admin123")
check("owner login", st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:120]}")
check("owner default next → /admin", json.loads(body).get("next")=="/admin", f"next={json.loads(body).get('next')!r}")
# Fresh per-run username so a leftover password change from a previous run
# cannot poison this run (create returns 409 silently if the user exists,
# leaving the old password in place and making the "change it now" assertions
# test yesterday's state instead of today's).
import time
pwuser = "pwuser_%d" % int(time.time())
INIT_PW = "User12345"
owner.req("/admin/users","POST",
          {"username":pwuser,"password":INIT_PW,"role":"user"})

# ── regular user ──
st, body, reg = login(pwuser,INIT_PW)
check("regular user default next → /admin/profile",
      json.loads(body).get("next")=="/admin/profile", f"next={json.loads(body).get('next')!r}")

# ── regular user can GET /admin/profile but is 403 elsewhere ──
st, b = reg.req("/admin/profile","GET")
check("regular user GET /admin/profile → 200", st==200, f"{st} {b[:80]}")
for path, want in [("/admin/users","403"), ("/admin/notifications","403"), ("/admin/passkeys","403")]:
    st, b = reg.req(path,"GET")
    check(f"regular user blocked from {path} → {want}", str(st)==want, f"{st} {b[:80]}")

# ── self-service password change (regular user) ──
st, b = reg.req("/admin/profile/password","PUT",{"old_password":"WRONGwrong1","new_password":"NewPass1234"})
check("wrong old password → 401", st==401, f"{st} {b[:100]}")
st, b = reg.req("/admin/profile/password","PUT",{"old_password":INIT_PW,"new_password":"weak"})
check("weak new password → 400", st==400, f"{st} {b[:100]}")
st, b = reg.req("/admin/profile/password","PUT",{"old_password":INIT_PW,"new_password":"User12345"})
check("new==old → 400", st==400, f"{st} {b[:100]}")
st, b = reg.req("/admin/profile/password","PUT",{"old_password":INIT_PW,"new_password":"NewUser12345"})
check("correct change → 200", st==200, f"{st} {b[:100]}")
# new password works for a fresh login
st, body, reg2 = login(pwuser,"NewUser12345")
check("login with new password", st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:100]}")

# ── owner all-user passkey listing ──
st, b = reg2.req("/admin/passkeys","GET")
check("regular user GET /admin/passkeys → 403 (owner-only)", st==403, f"{st} {b[:80]}")
st, b = owner.req("/admin/passkeys","GET")
check("owner GET /admin/passkeys → 200 list", st==200 and isinstance(json.loads(b), list), f"{st} {b[:120]}")

# empty creds list shape — both my-passkeys and owner-list behave sanely
st, b = reg2.req("/auth/passkey","GET")
check("regular user GET /auth/passkey (mine) → 200 array", st==200 and isinstance(json.loads(b), list), f"{st} {b[:80]}")

print(f"\n=== {PASS} passed, {FAIL} failed ===")
sys.exit(1 if FAIL else 0)