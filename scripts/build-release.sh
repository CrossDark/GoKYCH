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
#
# 用法：
#   scripts/build-release.sh                     # 用 git describe 推断版本
#   VERSION=v0.1.0 scripts/build-release.sh      # 显式指定
#   scripts/build-release.sh --upload             # 编译完直接 gh release create
#
# 前置：
#   - Go 1.22+（CGO_ENABLED=0 跨平台编译）
#   - gh（GitHub CLI）— 只有 --upload 才需要
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST="$REPO_ROOT/dist"

RED='\033[0;31m'; GRN='\033[0;32m'; YEL='\033[0;33m'; BLU='\033[0;34m'; NC='\033[0m'
log()  { printf "${BLU}==>${NC} %s\n" "$*"; }
ok()   { printf "${GRN}✓${NC} %s\n" "$*"; }
die()  { printf "${RED}✗${NC} %s\n" "$*" >&2; exit 1; }

UPLOAD=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --upload|-u) UPLOAD=1; shift ;;
    --help|-h)
      sed -n '2,28p' "$0"; exit 0 ;;
    *) die "未知参数: $1" ;;
  esac
done

# ── 1. 推断版本 ──
if [[ -z "${VERSION:-}" ]]; then
  if VERSION=$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null); then
    :
  else
    VERSION="dev"
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

# ── 5. release notes 模板（可选） ──
RELEASE_NOTES="$DIST/RELEASE_NOTES.md"
{
  echo "# gokych $VERSION"
  echo
  echo "Built on $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "From commit: $(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  echo
  echo "## Assets"
  echo
  echo "| File | Size |"
  echo "|------|------|"
  for pair in "${PLATFORMS[@]}"; do
    GOOS="${pair%|*}"; GOARCH="${pair#*|}"
    f="gokych-${GOOS}-${GOARCH}"
    [[ -f "$f" ]] && echo "| \`$f\` | $(du -h "$f" | cut -f1) |"
  done
  echo "| \`SHA256SUMS\` | $(wc -c < SHA256SUMS) bytes |"
  echo
  echo "## Install"
  echo
  echo '```bash'
  echo "curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | bash"
  echo '```'
} > "$RELEASE_NOTES"
ok "RELEASE_NOTES.md 已生成"

# ── 6. 上传到 GitHub Release（可选） ──
if [[ "$UPLOAD" -eq 1 ]]; then
  if ! command -v gh >/dev/null 2>&1; then
    die "gh (GitHub CLI) 未安装 — 跳过上传或者先 brew install gh"
  fi
  if ! gh auth status >/dev/null 2>&1; then
    die "gh 未登录 — 先 gh auth login"
  fi
  TAG="v${VERSION#v}"  # 把 v0.1.0 标准化
  if [[ "$VERSION" == "dev" || "$VERSION" == *-dirty ]]; then
    die "VERSION=$VERSION 不能上传 — 用 VERSION=v0.1.0 ./scripts/build-release.sh --upload"
  fi
  log "create/update release ${TAG}…"
  if gh release view "$TAG" >/dev/null 2>&1; then
    log "release 已存在，上传新资产…"
    gh release upload "$TAG" "$DIST"/* --clobber
  else
    log "创建新 release…"
    gh release create "$TAG" "$DIST"/* \
      --title "gokych $TAG" \
      --notes-file "$RELEASE_NOTES"
  fi
  ok "上传完成：https://github.com/CrossDark/GoKYCH/releases/tag/$TAG"
fi

ok "全部完成 🚀"
echo
echo "产物在 ${DIST}/，把下面的文件作为 asset 上传到 GitHub Release："
ls -1 "$DIST" | sed 's/^/  /'
