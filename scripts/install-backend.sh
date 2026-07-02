#!/usr/bin/env bash
# ────────────────────────────────────────────────────────────────────
# gokych 后端一键安装脚本（从 GitHub / GitCode Release 下载）
#
# 在 macOS 或 Ubuntu（任意平台）上跑，自动：
#   1. 检测当前 OS / 架构（uname）
#   2. 查 GitHub / GitCode 的最新 release
#   3. 下载匹配平台的二进制 + SHA256SUMS
#   4. 校验 hash
#   5. 改名 gokych，chmod +x
#   6. 装到 ${PREFIX:-/usr/local/bin}/gokych
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash
#
# 装到自定义目录（不需要 sudo）：
#   PREFIX=$HOME/.local curl ... | bash
#
# 强制只用某个 host（默认 auto，GitHub 失败才回退 gitcode）：
#   GOKYCH_HOST=gitcode curl ... | bash
#
# 装指定版本（默认 latest）：
#   GOKYCH_VERSION=v0.1.0 curl ... | bash
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

# ── 解析参数 ──
while [[ $# -gt 0 ]]; do
  case "$1" in
    --prefix=*)   PREFIX="${1#*=}"; shift ;;
    --host=*)     GOKYCH_HOST="${1#*=}"; shift ;;
    --version=*)  GOKYCH_VERSION="${1#*=}"; shift ;;
    --help|-h)
      sed -n '2,28p' "$0"; exit 0 ;;
    *) die "unknown arg: $1 (try --help)" ;;
  esac
done

PREFIX="${PREFIX:-/usr/local/bin}"
GOKYCH_HOST="${GOKYCH_HOST:-auto}"   # auto | github | gitcode
GOKYCH_VERSION="${GOKYCH_VERSION:-latest}"

# 仓库常量。两个平台的 repo name 大小写不一样 — 这是真值，别 "修"。
GH_REPO="CrossDark/GoKYCH"
GC_REPO="CrossDark/GoKych"
ASSET_PREFIX="gokych"
ASSET_SUFFIX_SUMS="SHA256SUMS"

# ── 1. 检测平台 ──
PLATFORM="$(detect_platform)"
GOOS="${PLATFORM%/*}"
GOARCH="${PLATFORM#*/}"
ASSET="${ASSET_PREFIX}-${GOOS}-${GOARCH}"
# NOTE: bash 3.2 (the default on macOS) mangles multi-byte UTF-8 chars in
# parameter expansion context — `$GOARCH（asset` reads as a separate var
# name because the high byte of the full-width paren gets glued to the
# var. So keep this log line ASCII-only around $VAR expansions.
log "platform: ${GOOS}/${GOARCH} (asset = ${ASSET})"

# ── 2. 取 release 信息 ──
fetch_release() {
  local host="$1"
  case "$host" in
    github)
      local url="https://api.github.com/repos/$GH_REPO/releases"
      if [[ "$GOKYCH_VERSION" == "latest" ]]; then
        url="$url/latest"
      else
        url="$url/tags/$GOKYCH_VERSION"
      fi
      # 用 curl 拉 JSON，jq 解析 tag_name 和 assets
      local json
      json=$(curl -fsSL -H "Accept: application/vnd.github+json" "$url" 2>/dev/null) || return 1
      TAG=$(echo "$json" | python3 -c "import json,sys;print(json.load(sys.stdin).get('tag_name',''))" 2>/dev/null) || return 1
      [[ -n "$TAG" ]] || return 1
      # 找匹配 asset 的 browser_download_url
      GH_DOWNLOAD_URL=$(echo "$json" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for a in d.get('assets', []):
    if a['name']=='$ASSET':
        print(a['browser_download_url']); break
" 2>/dev/null) || return 1
      GH_SUMS_URL=$(echo "$json" | python3 -c "
import json,sys
d=json.load(sys.stdin)
for a in d.get('assets', []):
    if a['name']=='$ASSET_SUFFIX_SUMS':
        print(a['browser_download_url']); break
" 2>/dev/null) || return 1
      [[ -n "$GH_DOWNLOAD_URL" && -n "$GH_SUMS_URL" ]] || return 1
      return 0
      ;;
    gitcode)
      # GitCode 的 release API 不一定 1:1 兼容 GitHub。给一个尝试：
      # https://gitcode.com/api/v3/repos/{owner}/{repo}/releases
      local url="https://gitcode.com/api/v3/repos/$GC_REPO/releases"
      if [[ "$GOKYCH_VERSION" == "latest" ]]; then
        url="$url/latest"
      else
        url="$url/tags/$GOKYCH_VERSION"
      fi
      local json
      json=$(curl -fsSL "$url" 2>/dev/null) || return 1
      TAG=$(echo "$json" | python3 -c "import json,sys;d=json.load(sys.stdin);print(d.get('tag_name','') or d.get('name',''))" 2>/dev/null) || return 1
      [[ -n "$TAG" ]] || return 1
      # GitCode asset 路径：https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{asset}
      local base="https://gitcode.com/$GC_REPO/releases/download/$TAG"
      GH_DOWNLOAD_URL="$base/$ASSET"
      GH_SUMS_URL="$base/$ASSET_SUFFIX_SUMS"
      return 0
      ;;
  esac
  return 1
}

# 试 GitHub → gitcode
TRIED=()
for host in github gitcode; do
  if [[ "$GOKYCH_HOST" != "auto" && "$GOKYCH_HOST" != "$host" ]]; then
    continue
  fi
  log "尝试从 $host 拉取 release…"
  if fetch_release "$host"; then
    RELEASE_HOST="$host"
    ok "从 $host 拿到 release $TAG"
    break
  fi
  TRIED+=("$host")
  warn "$host 没拿到 — 下个"
done
[[ -n "${RELEASE_HOST:-}" ]] || die "所有源都试过都失败（${TRIED[*]}）。设 GOKYCH_HOST=github|gitcode 显式指定，或手动下载。"

# ── 3. 下载 ──
WORK=$(mktemp -d)
trap "rm -rf $WORK" EXIT

log "downloading $ASSET ..."
if ! curl -fsSL -o "$WORK/$ASSET" "$GH_DOWNLOAD_URL"; then
  die "下载 $ASSET 失败 — 是不是 release 里没这个 asset？"
fi
log "downloading $ASSET_SUFFIX_SUMS ..."
if ! curl -fsSL -o "$WORK/$ASSET_SUFFIX_SUMS" "$GH_SUMS_URL"; then
  die "下载 $ASSET_SUFFIX_SUMS 失败"
fi
ok "下载完成"

# ── 4. SHA256 校验 ──
log "校验 SHA256…"
EXPECTED=$(grep -E "[[:space:]]\.?/?${ASSET}\$" "$WORK/$ASSET_SUFFIX_SUMS" | awk '{print $1}')
[[ -n "$EXPECTED" ]] || die "$ASSET_SUFFIX_SUMS 里找不到 $ASSET"
verify_sha256 "$WORK/$ASSET" "$EXPECTED"
ACTUAL="$EXPECTED"

# ── 5. 安装 ──
chmod +x "$WORK/$ASSET"
INSTALL_PATH="$PREFIX/gokych"
mkdir -p "$PREFIX"
# 已存在 → 备份
if [[ -f "$INSTALL_PATH" ]]; then
  warn "$INSTALL_PATH 已存在，备份为 $INSTALL_PATH.prev"
  cp "$INSTALL_PATH" "$INSTALL_PATH.prev"
fi
mv "$WORK/$ASSET" "$INSTALL_PATH"
ok "已安装到 $INSTALL_PATH"

# ── 6. 报告 ──
cat <<EOF

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
✓ gokych $TAG 已装到 $INSTALL_PATH
  源:   $RELEASE_HOST
  hash: $ACTUAL

接下来：

  # 1. 试跑一下（要 .env 配套 — 看 docs/deployment.md §2.4）
  $INSTALL_PATH

  # 2. 完整部署到 Ubuntu VM（systemd + nginx + MySQL + TLS）
  #    走 scripts/deploy-backend.sh，install 完再跑那个：
  REMOTE_HOST=user@api.example.com $INSTALL_PATH/deploy-backend.sh --update

  # 3. 或者手动 scp 上去：
  scp $INSTALL_PATH deploy@api.example.com:/opt/gokych/bin/gokych
  ssh deploy@api.example.com 'sudo systemctl restart gokych'

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
EOF
