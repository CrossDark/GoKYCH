#!/usr/bin/env python3
"""End-to-end tests for the unicode-allowing validation change.

Each user-session is isolated in its own CookieJar so creating + verifying
multiple unicode users in a row doesn't invalidate the owner's CSRF token.
URL paths containing unicode slugs are percent-encoded on the way out.
"""
import json, re, sys, urllib.parse, urllib.request, urllib.error
from http.cookiejar import CookieJar

BASE = "http://localhost:8111/api"
PASS = 0; FAIL = 0
def check(name, cond, detail=""):
    global PASS, FAIL
    if cond: PASS += 1; print(f"  \u2713 {name}")
    else: FAIL += 1; print(f"  \u2717 {name}  {detail}")

class Sess:
    def __init__(self):
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(CookieJar()))
        self.csrf = None
    def req(self, path, method="GET", body=None):
        h = {}; data = None
        if body is not None: data = json.dumps(body).encode("utf-8"); h["Content-Type"] = "application/json"
        if self.csrf: h["X-CSRF-Token"] = self.csrf
        r = urllib.request.Request(BASE+path, data=data, method=method, headers=h)
        try: resp = self.op.open(r); return resp.status, resp.read().decode("utf-8")
        except urllib.error.HTTPError as e: return e.code, e.read().decode("utf-8","replace")
    def fresh_csrf(self):
        st, body = self.req("/auth/csrf")
        self.csrf = json.loads(body)["csrf_token"]
        return json.loads(body)

def solve(q):
    m = re.match(r"(\d+)\s*([+\-×])\s*(\d+)", q)
    a,op,b = int(m.group(1)), m.group(2), int(m.group(3))
    return str(a+b if op=="+" else (a-b if op=="-" else a*b))
def login(user, pw):
    s = Sess(); j = s.fresh_csrf(); ans = solve(j["captcha"]["question"])
    st, body = s.req("/auth/login","POST",
                     {"username":user,"password":pw,"captcha":ans,"csrf_token":s.csrf})
    s.fresh_csrf()  # login rotated the token
    return st, body, s
def esc(slug): return urllib.parse.quote(slug, safe="")

# ── owner session for admin operations ──
st, body, owner = login("admin","admin123")
check("owner login", st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:100]}")

# ── special-char username rejected ──
st, body = owner.req("/admin/users","POST",{"username":"user@test","password":"Pass1234","role":"user"})
check("special-char username rejected (400 + 字母)", st==400 and "字母" in body, f"{st} {body[:120]}")

# ── unicode usernames accepted (create) AND round-trip login ──
for username in ["跨越晨昏", "Jérôme.OB", "Иван_2024"]:
    owner.req("/admin/users","POST",{"username":username,"password":"Pass1234","role":"user"})
    st, body, _ = login(username,"Pass1234")
    check(f"unicode username {username!r} login round-trips",
          st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:120]}")

# ── unicode password change + login ──
# Use a fresh unique username so a stale user from a prior test run can't
# mask a regression here (the create would 409-conflict silently and we'd
# be testing yesterday's password state).
import time
uniq = "unic_pw_%d" % int(time.time())
owner.req("/admin/users","POST",{"username":uniq,"password":"Pass1234","role":"user"})
st, body, u = login(uniq,"Pass1234")
check("fresh unicode-friendly user logs in", st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:120]}")
st, body = u.req("/admin/profile/password","PUT",{"old_password":"Pass1234","new_password":"密码🚀Aa1"})
check("unicode password accepted on change (200)", st==200, f"{st} {body[:120]}")
st, body, _ = login(uniq,"密码🚀Aa1")
check("login with unicode+emoji password", st==200 and json.loads(body).get("status")=="ok", f"{st} {body[:120]}")

# ── article slug with unicode accepted + reads back via %-encoded path ──
slug = "我第一篇笔记"
owner.req(f"/articles/typst/{esc(slug)}","DELETE",{"X-CSRF-Token":owner.csrf})
st, body = owner.req("/articles?type=md","POST",{"slug":slug,"title":"Unicode Slug Test","content":"# 测试 unicode 文章"})
check("unicode slug article created (201)", st==201, f"{st} {body[:120]}")
st, body = owner.req(f"/articles/md/{esc(slug)}","GET")
got = json.loads(body) if st==200 else {}
check("unicode slug article reads back",
      st==200 and got.get("article",{}).get("slug")==slug, f"{st} {body[:140]}")

# ── slug rejections ──
for bad, why in [("a/b","slash"),("100%-done","percent"),("测试🚀","emoji"),("a b","space"),(".","dot-seg"),("..","dot-dot-seg")]:
    st, body = owner.req("/articles?type=md","POST",{"slug":bad,"title":"x","content":"y"})
    check(f"slug {why!r} rejected (400)", st==400, f"{st} {body[:120]}")

# cleanup
owner.req(f"/articles/md/{esc(slug)}","DELETE",{"X-CSRF-Token":owner.csrf})

print(f"\n=== {PASS} passed, {FAIL} failed ===")
sys.exit(1 if FAIL else 0)