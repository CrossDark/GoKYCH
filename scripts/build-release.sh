#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych 多平台发布构建脚本
#
# 从 main（或当前分支 HEAD）打出 4 个平台的二进制到 dist/，每个都
# 带 SHA256，方便 scripts/install-backend.sh 下载后校验。维护者
# 把 dist/ 下的文件作为 asset 上传到 GitHub Release / GitCode Release
# 即可。
#
# 产物：
#   dist/gokych-darwin-amd64    (Intel Mac)
#   dist/gokych-darwin-arm64    (Apple Silicon)
#   dist/gokych-linux-amd64     (Ubuntu/Debian x86_64)
#   dist/gokych-linux-arm64     (Ubuntu ARM / Graviton)
#   dist/SHA256SUMS             (所有 binary 的 sha256，install 脚本会读)
#   dist/RELEASE_NOTES.md       (中英双语 release notes)
#
# 用法：
#   scripts/build-release.sh v0.1.0                # CLI 位置参数
#   scripts/build-release.sh --version v0.1.0       # CLI flag
#   scripts/build-release.sh -v v0.1.0 --upload     # 短 flag + 直接上传
#   scripts/build-release.sh                       # 不指定版本 → git describe
#   VERSION=v0.1.0 scripts/build-release.sh        # 环境变量（兼容旧用法）
#
# --upload 模式：若未通过 --version / -v / VERSION env 显式指定版本，
#   交互式 TTY → 主动 read 询问输入（git describe 拿到合法 tag 时可回车确认）；
#   非 TTY (CI / pipe) → 直接 die 并提示用 --version / -v 显式传。
#
# 上传目标（--upload）：
#   - GitHub:  使用 gh CLI（必须已登录）
#   - GitCode: 使用 curl 调用 API
#     - GITCODE_TOKEN  （必填）: 私人令牌，用于 v5 API 创建 release
#       在 https://gitcode.com/setting/provate-tokens 创建（需 projects 权限）
#     - GITCODE_COOKIE （可选）: 浏览器会话 Cookie，用于 v2 API 上传二进制附件
#       未设置时仅创建 release 和 tag，不上传二进制文件
#       获取方法: 登录 gitcode.com → F12 → Application → Cookies → 复制所有 cookie
#     同时自动推送 tag 到 GitCode remote（如果配置了的话）
#
# 前置：
#   - Go 1.26+（CGO_ENABLED=0 跨平台编译）
#   - gh（GitHub CLI）— 只有上传 GitHub 时需要
#   - python3（macOS/Linux 通常预装，用于 JSON 解析）
#   - GITCODE_TOKEN 环境变量 — 上传 GitCode 时必填
#   - GITCODE_COOKIE 环境变量 — 上传 GitCode 二进制附件时需要
#
# 版本格式：vX.Y.Z 或 X.Y.Z（可带 -rc1 / -alpha.1 等 pre-release 后缀）
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST="$REPO_ROOT/dist"

source "$SCRIPT_DIR/lib.sh"

# ── 校验 semver-ish 字符串 ──
# 接受: v0.1.0, 0.1.0, 0.1.0-rc1, 0.1.0-alpha.1, 1.2.3+build.5
# is_valid_version: return 0/1, 不 die
is_valid_version() {
  local v="$1"
  [[ -z "$v" ]] && return 1
  local body="${v#v}"
  [[ "$body" =~ ^[0-9]+(\.[0-9]+){1,2}([-+][A-Za-z0-9.-]+)?$ ]]
}

# validate_version: 不合法则 die
validate_version() {
  local v="$1"
  is_valid_version "$v" || die "VERSION='$v' 不是合法 semver (e.g. v0.1.0, 0.1.0, 0.1.0-rc1)"
}

# ── 解析参数 ──
VERSION_ARG=""
UPLOAD=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version=*)   VERSION_ARG="${1#*=}"; shift ;;
    --version|-v)  VERSION_ARG="${2:-}"; shift 2 ;;
    --upload|-u)   UPLOAD=1; shift ;;
    --help|-h)
      sed -n '2,32p' "$0"; exit 0 ;;
    --upload=*)    UPLOAD=1; shift ;;
    v[0-9]*|[0-9]*)
      if [[ -n "$VERSION_ARG" ]]; then
        die "重复指定版本号: '$VERSION_ARG' 和 '$1'"
      fi
      VERSION_ARG="$1"; shift ;;
    *)             die "未知参数: $1 (--help 看用法)" ;;
  esac
done

# ── 1. 推断版本（优先级: CLI 参数 > VERSION 环境变量 > git describe > dev） ──
VERSION_EXPLICIT=0
if [[ -n "$VERSION_ARG" ]]; then
  validate_version "$VERSION_ARG"
  VERSION="$VERSION_ARG"
  VERSION_EXPLICIT=1
elif [[ -n "${VERSION:-}" ]]; then
  validate_version "$VERSION"
  VERSION_EXPLICIT=1
elif VERSION=$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null); then
  :  # git describe 拿到的不一定合法 semver（可能是 commit hash），留待后续检查
else
  VERSION="dev"
fi

# ── 1.5 --upload 模式: 若版本不是合法 semver，要求明确输入 ──
# dev、*-dirty、纯 commit hash 等都不能直接上传
# 交互式 TTY 走 read 询问（带当前值作为默认）；非 TTY (CI / pipe) 直接 die
if [[ "$UPLOAD" -eq 1 && "$VERSION_EXPLICIT" -eq 0 ]]; then
  echo
  if is_valid_version "$VERSION"; then
    # git describe 给出了合法 tag，询问是否确认
    printf "${YEL}==>${NC} --upload 检测到版本: ${GRN}%s${NC}  (按回车确认,或输入新版本号)\n" "$VERSION"
    DEFAULT_VER="$VERSION"
  else
    printf "${YEL}==>${NC} --upload 需要明确版本号,当前自动推断为: ${YEL}%s${NC}\n" "$VERSION"
    DEFAULT_VER=""
  fi
  if [[ -t 0 ]]; then
    while :; do
      if ! read -r -p "$(printf '%s==>%s 输入版本号 (e.g. v0.1.0, 0.1.0-rc1)%s: ' "${BLU}" "${NC}" "${DEFAULT_VER:+ [默认: $DEFAULT_VER]}")" INPUT_VER; then
        die "stdin EOF,无法询问版本号(用 --version=v0.1.0 或 VERSION=v0.1.0 env 传)"
      fi
      # 用户直接回车 → 使用默认值（如果有）
      [[ -z "$INPUT_VER" && -n "$DEFAULT_VER" ]] && INPUT_VER="$DEFAULT_VER"
      [[ -z "$INPUT_VER" ]] && { printf '  %s版本号不能为空,重新输入 (Ctrl-C 取消)%s\n' "${YEL}" "${NC}"; continue; }
      if is_valid_version "$INPUT_VER"; then
        break
      else
        printf '  %s%q 不是合法 semver,重新输入 (e.g. v0.1.0, 0.1.0-rc1)%s\n' "${YEL}" "$INPUT_VER" "${NC}"
      fi
    done
    VERSION="$INPUT_VER"
  else
    die "--upload 需要明确版本号 (非交互环境,无法询问);用 --version=v0.1.0 或 VERSION=v0.1.0 env 传"
  fi
fi

log "版本: $VERSION"

# 平台 → GOOS/GOARCH 映射。用 | 分隔（避免空格歧义）。
PLATFORMS=(
  "darwin|amd64"
  "darwin|arm64"
  "linux|amd64"
  "linux|arm64"
)

# ── 2. 清理 + 建目录 ──
rm -rf "$DIST"
mkdir -p "$DIST"
ok "dist/ 已就绪"

# ── 3. 编译 ──
cd "$REPO_ROOT"
LDFLAGS="-s -w -X main.version=$VERSION"
for pair in "${PLATFORMS[@]}"; do
  GOOS="${pair%|*}"
  GOARCH="${pair#*|}"
  OUT="$DIST/gokych-${GOOS}-${GOARCH}"
  log "编译 $GOOS/$GOARCH → $(basename "$OUT")"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
    go build -ldflags="$LDFLAGS" -trimpath -o "$OUT" ./cmd/gokych
  chmod +x "$OUT"
  ok "$OUT ($(du -h "$OUT" | cut -f1))"
done

# ── 4. SHA256SUMS ──
log "生成 SHA256SUMS…"
cd "$DIST"
# 标准 sha256sum 格式：<hash>  <filename>，文件名带 ./ 前缀以兼容 BSD
for f in gokych-*; do
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$f"
  else
    # macOS 没有 sha256sum，用 shasum -a 256
    shasum -a 256 "$f"
  fi
done | sed 's|  |  ./|' > SHA256SUMS
ok "SHA256SUMS 已生成"
cat SHA256SUMS

# ── 5. release notes 模板（中英双语 + 推荐 install-all.sh） ──
RELEASE_NOTES="$DIST/RELEASE_NOTES.md"
BUILT_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMMIT_SHA="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
TAG_DISPLAY="v${VERSION#v}"

{
  cat <<HEADER
# gokych ${VERSION}

> 🇨🇳 **中文**:GoKYCH 后端的多平台预编译二进制 — 包含 SHA256 校验和。
> 🇬🇧 **English**:Multi-platform prebuilt binaries for the GoKYCH backend — SHA256 sums included.

---

## 📦 Assets / 资产文件

| File / 文件 | Size / 大小 | SHA256 (前 12 位) |
|------|------|------|
HEADER

  # 读 SHA256SUMS 写资产表（数据源唯一,从 SHA256SUMS 来）
  while read -r hash file; do
    file="${file#./}"
    [[ -f "$file" ]] || continue
    short="${hash:0:12}"
    printf "| \`%s\` | %s | \`%s…\` |\n" \
      "$file" "$(du -h "$file" | cut -f1)" "$short"
  done < SHA256SUMS

  cat <<TABLE_END

完整校验码见同目录下的 \`SHA256SUMS\` 文件(或本 Release 的同名 asset)。
Full checksums: see the \`SHA256SUMS\` file in this release.

---

## 🚀 一键部署 / One-Click Install (推荐 / Recommended)

> **全新 Ubuntu 22.04/24.04 VM 的首选** — 一行命令搞定 MySQL、nginx、certbot、systemd、TLS、健康检查。
> **Recommended for fresh Ubuntu 22.04/24.04 VMs** — one command installs MySQL, nginx, certbot, systemd, TLS, and runs health checks.

\`\`\`bash
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-all.sh | \\
  sudo bash -s -- \\
    --site-name "My Site / 我的网站" \\
    --api-domain "api.example.com" \\
    --main-domain "example.com" \\
    --frontend-domain "www.example.com" \\
    --email "admin@example.com" \\
    --admin-password "ChangeMe-Strong-Pwd"
\`\`\`

更多参数(\`--release\`, \`--host\`, \`--yes\`, \`--uninstall\` 等)见 \`bash install-all.sh --help\`。
See \`bash install-all.sh --help\` for more flags.

---

## 🛠️ 单机安装 / Single-Machine Install

> 适合 macOS 本地、临时测试、容器 — 只装二进制,不做服务端初始化。
> For macOS dev machines, ad-hoc testing, or containers — installs the binary only, no server init.

\`\`\`bash
# 默认:GitHub Release 拉最新 + sha256 校验
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash

# 装到用户目录(不用 sudo)
PREFIX=\$HOME/.local curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash
\`\`\`

---

## 📝 Build Info / 构建信息

- **Tag / 版本**: \`${TAG_DISPLAY}\`
- **Commit / 提交**: \`${COMMIT_SHA}\`
- **Built at / 构建时间**: \`${BUILT_AT}\`
- **Frontend / 前端**: 由 [EdgeOne Makers](docs/deployment.md) 自动构建,不在此 release 中。
TABLE_END
} > "$RELEASE_NOTES"
ok "RELEASE_NOTES.md 已生成 (中英双语 + 推荐 install-all.sh)"

# ── GitCode 仓库常量（大小写敏感，与 remote URL 一致） ──
GC_OWNER="CrossDark"
GC_REPO="GoKych"
GC_API_V5="https://gitcode.com/api/v5"
GC_API_V2="https://web-api.gitcode.com/api/v2"
GC_REPO_ID="${GC_OWNER}%2F${GC_REPO}"
GC_REMOTE="GitCode"
GC_UA="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

# ── 6. 上传到 GitHub / GitCode Release（可选） ──
if [[ "$UPLOAD" -eq 1 ]]; then
  TAG="v${VERSION#v}"  # 把 v0.1.0 标准化
  # 防御性二次检查:第 1.5 步 --upload 已 prompt 过,这里再卡一次
  is_valid_version "$VERSION" || die "VERSION=$VERSION 仍不是合法 semver — 用 --version v0.1.0 / -v v0.1.0 / VERSION=v0.1.0 env 显式传"

  # ── 6a. 确保 tag 存在并推送到两个 remote（release 依赖 tag 存在） ──
  log "处理 tag ${TAG}…"
  if git -C "$REPO_ROOT" rev-parse "$TAG" >/dev/null 2>&1; then
    log "tag ${TAG} 已存在本地"
  else
    log "tag ${TAG} 不存在，创建 annotated tag…"
    git -C "$REPO_ROOT" tag -a "$TAG" -m "release ${TAG}"
    ok "tag ${TAG} 已创建"
  fi
  log "推送 tag ${TAG} 到 remotes…"
  git -C "$REPO_ROOT" push origin "$TAG" 2>&1 | sed 's/^/  [origin] /' || warn "推送 tag 到 GitHub 失败（可能无权限或网络问题）"
  if git -C "$REPO_ROOT" remote get-url "$GC_REMOTE" >/dev/null 2>&1; then
    git -C "$REPO_ROOT" push "$GC_REMOTE" "$TAG" 2>&1 | sed 's/^/  [gitcode] /' || warn "推送 tag 到 GitCode 失败（可能无权限或网络问题）"
  else
    warn "GitCode remote 未配置，跳过推 tag（配置: git remote add GitCode https://gitcode.com/CrossDark/GoKych.git）"
  fi

  # ── 6b. GitHub 上传 ──
  if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    log "上传到 GitHub…"
    if gh release view "$TAG" >/dev/null 2>&1; then
      log "GitHub release 已存在，上传新资产…"
      gh release upload "$TAG" "$DIST"/* --clobber 2>&1 | sed 's/^/  /'
    else
      log "创建 GitHub release…"
      gh release create "$TAG" "$DIST"/* \
        --title "gokych $TAG" \
        --notes-file "$RELEASE_NOTES" 2>&1 | sed 's/^/  /'
    fi
    ok "GitHub 上传完成：https://github.com/CrossDark/GoKYCH/releases/tag/$TAG"
  else
    warn "gh (GitHub CLI) 未安装或未登录，跳过 GitHub 上传"
  fi

  # ── 6c. GitCode 上传 ──
  # GitCode 有两套 API：
  #   v5 API (Gitee 兼容):  用 GITCODE_TOKEN（私人令牌）认证，支持创建 release，
  #                         但响应不含数字 id，attach_files 端点也未实现（返回404）。
  #   v2 API (Web API):     需要 GITCODE_COOKIE（浏览器会话 Cookie）认证，
  #                         支持 release CRUD 和两阶段附件上传（OBS 预签名）。
  #
  # 两阶段附件上传流程：
  #   1. POST /api/v2/projects/{repoId}/releases/upload  → 获取 OBS 预签名 URL
  #   2. PUT  <signed-url>                                 → 上传文件到对象存储
  #   3. PUT  /api/v2/projects/{repoId}/releases/{tag}    → 更新 release 的 links 关联附件
  #
  # 获取 GITCODE_COOKIE：
  #   登录 gitcode.com → F12 打开开发者工具 → Application/存储 → Cookies →
  #   复制 gitcode.com 下所有 cookie 的 name=value 对（分号分隔）。
  #   Cookie 有效期约数周，过期后需重新获取。
  if [[ -n "${GITCODE_TOKEN:-}" ]]; then
    log "上传到 GitCode…"

    # 先确保 release 存在（v5 API 创建）
    GC_EXIST_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
      -H "User-Agent: $GC_UA" \
      "${GC_API_V5}/repos/${GC_OWNER}/${GC_REPO}/releases/tags/${TAG}" 2>/dev/null)

    if [[ "$GC_EXIST_CODE" == "200" ]]; then
      log "GitCode release ${TAG} 已存在"
    else
      log "创建 GitCode release ${TAG}（v5 API）…"
      GC_CREATE_BODY=$(curl -s -w "\n%{http_code}" -X POST \
        -H "User-Agent: $GC_UA" \
        -F "access_token=${GITCODE_TOKEN}" \
        -F "tag_name=${TAG}" \
        -F "name=gokych ${TAG}" \
        -F "body=<${RELEASE_NOTES}" \
        -F "target_commitish=$(git -C "$REPO_ROOT" rev-parse HEAD)" \
        -F "prerelease=false" \
        "${GC_API_V5}/repos/${GC_OWNER}/${GC_REPO}/releases" 2>&1) || true
      GC_CREATE_CODE=$(echo "$GC_CREATE_BODY" | tail -1)
      GC_CREATE_RESP=$(echo "$GC_CREATE_BODY" | sed '$d')
      if [[ "$GC_CREATE_CODE" == "201" || "$GC_CREATE_CODE" == "200" ]]; then
        ok "GitCode release 创建成功"
      else
        warn "GitCode 创建 release 响应 HTTP ${GC_CREATE_CODE}: $(echo "$GC_CREATE_RESP" | head -c 200)"
      fi
    fi

    # ── 上传二进制附件（需要 GITCODE_COOKIE） ──
    if [[ -n "${GITCODE_COOKIE:-}" ]]; then
      if ! command -v python3 >/dev/null 2>&1; then
        warn "python3 未安装，跳过 GitCode 二进制附件上传"
      else
        # 获取 release 的当前状态（v2 GET，浏览器 UA 即可）
        GC_GET_BODY=$(curl -s -w "\n%{http_code}" \
          -H "User-Agent: $GC_UA" \
          -H "Accept: application/json" \
          -H "Referer: https://gitcode.com/" \
          "${GC_API_V2}/projects/${GC_REPO_ID}/releases/${TAG}" 2>&1) || true
        GC_GET_CODE=$(echo "$GC_GET_BODY" | tail -1)
        GC_GET_RESP=$(echo "$GC_GET_BODY" | sed '$d')

        if [[ "$GC_GET_CODE" != "200" ]]; then
          warn "获取 GitCode release 信息失败 (HTTP ${GC_GET_CODE})，跳过附件上传"
        else
          # 提取现有 links（避免重复添加）
          GC_EXISTING_LINKS=$(GC_GET_RESP="$GC_GET_RESP" python3 -c '
import os, json, re
d = os.environ.get("GC_GET_RESP", "")
try:
    d = re.sub(r"[\x00-\x08\x0b\x0c\x0e-\x1f]", " ", d)
    r = json.loads(d)
    links = []
    for l in ((r.get("assets") or {}).get("links") or []):
        links.append({"name": l.get("name"), "url": l.get("url"), "attachment_id": l.get("attachment_id")})
    print(json.dumps(links))
except Exception:
    print("[]")
' 2>/dev/null || echo '[]')

          GC_LINKS_FILE="$(mktemp)"
          echo '[]' > "$GC_LINKS_FILE"
          # 注意: 此处不使用 trap 清理临时文件，避免覆盖外层 trap

          for f in "$DIST"/*; do
            fname="$(basename "$f")"
            fsize=$(stat -f%z "$f" 2>/dev/null || stat -c%s "$f" 2>/dev/null)

            case "$fname" in
              *.sha256|SHA256SUMS|*.md) ftype="text/plain" ;;
              *)                       ftype="application/octet-stream" ;;
            esac

            printf "  上传 %s (%s) … " "$fname" "$(du -h "$f" | cut -f1)"

            # Step 1: 获取 OBS 预签名 URL
            SIGNED_BODY=$(curl -s -w "\n%{http_code}" -X POST \
              -H "User-Agent: $GC_UA" \
              -H "Accept: application/json" \
              -H "Content-Type: application/json" \
              -H "Referer: https://gitcode.com/${GC_OWNER}/${GC_REPO}/releases/new" \
              -H "Origin: https://gitcode.com" \
              -H "Cookie: ${GITCODE_COOKIE}" \
              -d "{\"type\":\"RELEASE\",\"size\":${fsize},\"file_name\":\"${fname}\",\"content_type\":\"${ftype}\",\"project_id\":\"${GC_REPO_ID}\"}" \
              "${GC_API_V2}/projects/${GC_REPO_ID}/releases/upload" 2>&1) || true
            SIGNED_CODE=$(echo "$SIGNED_BODY" | tail -1)
            SIGNED_RESP=$(echo "$SIGNED_BODY" | sed '$d')

            if [[ "$SIGNED_CODE" != "200" ]]; then
              echo "✗ (签名 URL HTTP ${SIGNED_CODE})"
              echo "    $(echo "$SIGNED_RESP" | head -c 200)"
              continue
            fi

            # 用 python3 解析签名响应
            SIGNED_FIELDS=$(SIGNED_RESP="$SIGNED_RESP" GC_OWNER="$GC_OWNER" GC_REPO="$GC_REPO" TAG="$TAG" FNAME="$fname" python3 -c '
import os, json, sys
from urllib.parse import quote
d = os.environ.get("SIGNED_RESP", "")
try:
    j = json.loads(d)
    if "error_code" in j:
        sys.exit(1)
    urls = [k for k in j.keys() if k.startswith("http")]
    if not urls:
        sys.exit(1)
    signed_url = urls[0]
    info = j[signed_url]
    owner = os.environ["GC_OWNER"]
    repo = os.environ["GC_REPO"]
    tag = os.environ["TAG"]
    fn = os.environ["FNAME"]
    cdn = "https://gitcode.com/{}/{}/releases/download/{}/{}".format(owner, repo, tag, quote(fn))
    out = {
        "signedUrl": signed_url,
        "attachmentId": info.get("attachment_id", ""),
        "cdnAddr": cdn,
        "contentType": info.get("Content-Type", "application/octet-stream"),
        "obsAcl": info.get("x-obs-acl", "private"),
        "obsMetaProjectId": info.get("x-obs-meta-project-id", ""),
        "obsCallback": info.get("x-obs-callback", ""),
    }
    for k, v in out.items():
        print("{}={}".format(k.upper(), json.dumps(v)))
except Exception as e:
    print("PARSE_ERROR={}".format(json.dumps(str(e))))
' 2>/dev/null) || true

            if [[ -z "$SIGNED_FIELDS" || "$SIGNED_FIELDS" == *PARSE_ERROR* ]]; then
              echo "✗ (解析签名响应失败)"
              continue
            fi

            eval "$SIGNED_FIELDS"

            if [[ -z "${SIGNEDURL:-}" || -z "${ATTACHMENTID:-}" ]]; then
              echo "✗ (解析签名 URL 失败)"
              continue
            fi

            # Step 2: PUT 文件到 OBS
            PUT_EXTRA_HEADERS=()
            [[ -n "${OBSMETAPROJECTID:-}" ]] && PUT_EXTRA_HEADERS+=(-H "x-obs-meta-project-id: ${OBSMETAPROJECTID}")
            [[ -n "${OBSCALLBACK:-}" ]] && PUT_EXTRA_HEADERS+=(-H "x-obs-callback: ${OBSCALLBACK}")

            PUT_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
              -H "Content-Type: ${CONTENTTYPE:-$ftype}" \
              -H "x-obs-acl: ${OBSACL:-private}" \
              "${PUT_EXTRA_HEADERS[@]}" \
              --data-binary "@${f}" \
              "$SIGNEDURL" 2>/dev/null) || true

            if [[ "$PUT_CODE" -ge 200 && "$PUT_CODE" -lt 300 ]]; then
              echo "✓"
              GC_LINKS_FILE="$GC_LINKS_FILE" FNAME="$fname" CDN_URL="$CDNADDR" ATTACH_ID="$ATTACHMENTID" python3 -c '
import os, json
f = os.environ["GC_LINKS_FILE"]
arr = json.loads(open(f).read() or "[]")
arr.append({"name": os.environ["FNAME"], "url": os.environ["CDN_URL"], "attachment_id": os.environ["ATTACH_ID"], "action": "create"})
open(f, "w").write(json.dumps(arr))
'
            else
              echo "✗ (OBS HTTP ${PUT_CODE})"
            fi
          done

          # Step 3: 合并 links 并 PUT 更新 release
          GC_NEW_LINKS=$(cat "$GC_LINKS_FILE")
          rm -f "$GC_LINKS_FILE"

          if [[ "$GC_NEW_LINKS" != "[]" ]]; then
            log "关联附件到 release…"

            GC_MERGED_LINKS=$(GC_EXISTING_LINKS="$GC_EXISTING_LINKS" GC_NEW_LINKS="$GC_NEW_LINKS" python3 -c '
import os, json
existing = json.loads(os.environ.get("GC_EXISTING_LINKS", "[]"))
new_links = json.loads(os.environ.get("GC_NEW_LINKS", "[]"))
new_names = {l["name"] for l in new_links}
merged = [l for l in existing if l.get("name") not in new_names] + new_links
cleaned = [{"name": l["name"], "url": l["url"], "attachment_id": l["attachment_id"], "action": l.get("action", "create")} for l in merged]
print(json.dumps(cleaned))
' 2>/dev/null || echo "$GC_NEW_LINKS")

            # 用 python3 构建完整 PUT body
            GC_PUT_BODY=$(TAG="$TAG" RELEASE_NOTES="$RELEASE_NOTES" GC_MERGED_LINKS="$GC_MERGED_LINKS" python3 -c '
import os, json
desc = open(os.environ["RELEASE_NOTES"]).read()
links = json.loads(os.environ.get("GC_MERGED_LINKS", "[]"))
body = {
    "tag_name": os.environ["TAG"],
    "name": "gokych " + os.environ["TAG"],
    "description": desc,
    "links": links,
    "assets": []
}
print(json.dumps(body))
' 2>/dev/null)

            PUT_REL_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X PUT \
              -H "User-Agent: $GC_UA" \
              -H "Accept: application/json" \
              -H "Content-Type: application/json" \
              -H "Referer: https://gitcode.com/${GC_OWNER}/${GC_REPO}/releases/edit/${TAG}" \
              -H "Origin: https://gitcode.com" \
              -H "Cookie: ${GITCODE_COOKIE}" \
              -d "$GC_PUT_BODY" \
              "${GC_API_V2}/projects/${GC_REPO_ID}/releases/${TAG}" 2>/dev/null) || true

            if [[ "$PUT_REL_CODE" -ge 200 && "$PUT_REL_CODE" -lt 300 ]]; then
              ok "GitCode 附件上传完成：https://gitcode.com/${GC_OWNER}/${GC_REPO}/releases/tag/${TAG}"
            else
              warn "关联附件到 release 失败 (HTTP ${PUT_REL_CODE})，文件已上传但未关联"
            fi
          else
            warn "没有文件成功上传到 OBS"
          fi
        fi
      fi
    else
      warn "GITCODE_COOKIE 未设置，跳过 GitCode 二进制附件上传"
      warn "（仅创建了 release 和 tag。要上传二进制，请设置 GITCODE_COOKIE 环境变量）"
      warn "获取方法：登录 gitcode.com → F12 → Application → Cookies → 复制所有 cookie"
    fi
  else
    warn "GITCODE_TOKEN 未设置，跳过 GitCode 上传（设置: export GITCODE_TOKEN=你的私人令牌）"
  fi
fi

ok "全部完成 🚀"
echo
echo "产物在 ${DIST}/，把下面的文件作为 asset 上传到 GitHub/GitCode Release："
ls -1 "$DIST" | sed 's/^/  /'
