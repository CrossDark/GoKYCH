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
#   scripts/build-release.sh v0.1.0                # CLI 位置参数
#   scripts/build-release.sh --version v0.1.0       # CLI flag
#   scripts/build-release.sh -v v0.1.0 --upload     # 短 flag + 直接上传
#   scripts/build-release.sh                       # 不指定版本 → git describe
#   VERSION=v0.1.0 scripts/build-release.sh        # 环境变量（兼容旧用法）
#
# --upload 模式：若版本未定（dev / *-dirty），
#   交互式 TTY → 主动 read 询问输入；
#   非 TTY (CI / pipe) → 直接 die 并提示用 --version / -v 显式传。
#
# 前置：
#   - Go 1.22+（CGO_ENABLED=0 跨平台编译）
#   - gh（GitHub CLI）— 只有 --upload 才需要
#
# 版本格式：vX.Y.Z 或 X.Y.Z（可带 -rc1 / -alpha.1 等 pre-release 后缀）
# ────────────────────────────────────────────────────────────────────
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DIST="$REPO_ROOT/dist"

RED='\033[0;31m'; GRN='\033[0;32m'; YEL='\033[0;33m'; BLU='\033[0;34m'; NC='\033[0m'
log()  { printf "${BLU}==>${NC} %s\n" "$*"; }
ok()   { printf "${GRN}✓${NC} %s\n" "$*"; }
die()  { printf "${RED}✗${NC} %s\n" "$*" >&2; exit 1; }

# ── 校验 semver-ish 字符串 ──
# 接受: v0.1.0, 0.1.0, 0.1.0-rc1, 0.1.0-alpha.1, 1.2.3+build.5
validate_version() {
  local v="$1"
  # 空 — 由调用方决定是否回退
  [[ -z "$v" ]] && return 0
  # 可选 'v' 前缀, 然后 X.Y.Z（1-3 段数字）, 可选 pre-release / build
  local body="${v#v}"
  if ! [[ "$body" =~ ^[0-9]+(\.[0-9]+){1,2}([-+][A-Za-z0-9.-]+)?$ ]]; then
    die "VERSION='$v' 不是合法 semver (e.g. v0.1.0, 0.1.0, 0.1.0-rc1)"
  fi
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
if [[ -n "$VERSION_ARG" ]]; then
  validate_version "$VERSION_ARG"
  VERSION="$VERSION_ARG"
elif [[ -n "${VERSION:-}" ]]; then
  validate_version "$VERSION"
elif VERSION=$(git -C "$REPO_ROOT" describe --tags --always --dirty 2>/dev/null); then
  :  # git describe 拿到的不一定合法 semver,留待 upload 阶段拒绝 dev/dirty
else
  VERSION="dev"
fi

# ── 1.5 --upload 模式: 若版本未定,要求明确输入 ──
# dev / *-dirty (无 tag 或有未提交改动) 都不能直接上传
# 交互式 TTY 走 read 询问;非 TTY (CI / curl|bash) 直接 die 给出明确指引
if [[ "$UPLOAD" -eq 1 && ( "$VERSION" == "dev" || "$VERSION" == *-dirty ) ]]; then
  echo
  printf "${YEL}==>${NC} --upload 需要明确版本号,目前解析为: ${YEL}%s${NC}\n" "$VERSION"
  if [[ -t 0 ]]; then
    while :; do
      if ! read -r -p "$(printf '%s==>%s 输入版本号 (e.g. v0.1.0, 0.1.0-rc1): ' "${BLU}" "${NC}")" INPUT_VER; then
        die "stdin EOF,无法询问版本号(用 --version=v0.1.0 或 VERSION=v0.1.0 env 传)"
      fi
      [[ -z "$INPUT_VER" ]] && { printf '  %s版本号不能为空,重新输入 (Ctrl-C 取消)%s\n' "${YEL}" "${NC}"; continue; }
      if validate_version "$INPUT_VER" 2>/dev/null; then
        # validate_version 成功会 return 0,失败会 die;这里都返回了说明 OK
        break
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

# ── 6. 上传到 GitHub Release（可选） ──
if [[ "$UPLOAD" -eq 1 ]]; then
  if ! command -v gh >/dev/null 2>&1; then
    die "gh (GitHub CLI) 未安装 — 跳过上传或者先 brew install gh"
  fi
  if ! gh auth status >/dev/null 2>&1; then
    die "gh 未登录 — 先 gh auth login"
  fi
  TAG="v${VERSION#v}"  # 把 v0.1.0 标准化
  # 防御性二次检查:第 1.5 步 --upload 已 prompt 过,这里再卡一次
  if [[ "$VERSION" == "dev" || "$VERSION" == *-dirty ]]; then
    die "VERSION=$VERSION 仍不能上传 — 用 --version v0.1.0 / -v v0.1.0 / VERSION=v0.1.0 env 显式传"
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
