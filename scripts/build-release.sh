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
#   - GitCode: 使用 curl + GITCODE_TOKEN 环境变量（私人令牌，需 projects 权限）
#              同时自动推送 tag 到 GitCode remote（如果配置了的话）
#
# 前置：
#   - Go 1.26+（CGO_ENABLED=0 跨平台编译）
#   - gh（GitHub CLI）— 只有上传 GitHub 时需要
#   - GITCODE_TOKEN 环境变量 — 只有上传 GitCode 时需要
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
GC_API="https://gitcode.com/api/v5"
GC_REMOTE="GitCode"

# ── 6. 上传到 GitHub / GitCode Release（可选） ──
if [[ "$UPLOAD" -eq 1 ]]; then
  TAG="v${VERSION#v}"  # 把 v0.1.0 标准化
  # 防御性二次检查:第 1.5 步 --upload 已 prompt 过,这里再卡一次
  is_valid_version "$VERSION" || die "VERSION=$VERSION 仍不是合法 semver — 用 --version v0.1.0 / -v v0.1.0 / VERSION=v0.1.0 env 显式传"

  # ── 6a. 先推送 tag 到两个 remote（release 依赖 tag 存在） ──
  log "推送 tag ${TAG} 到 remotes…"
  if git -C "$REPO_ROOT" rev-parse "$TAG" >/dev/null 2>&1; then
    # tag 已存在本地，直接推送
    git -C "$REPO_ROOT" push origin "$TAG" 2>&1 | sed 's/^/  [origin] /' || warn "推送 tag 到 GitHub 失败（可能无权限或网络问题）"
    if git -C "$REPO_ROOT" remote get-url "$GC_REMOTE" >/dev/null 2>&1; then
      git -C "$REPO_ROOT" push "$GC_REMOTE" "$TAG" 2>&1 | sed 's/^/  [gitcode] /' || warn "推送 tag 到 GitCode 失败（可能无权限或网络问题）"
    else
      warn "GitCode remote 未配置，跳过推 tag（配置: git remote add GitCode https://gitcode.com/CrossDark/GoKych.git）"
    fi
  else
    warn "tag ${TAG} 不存在于本地 — 跳过自动推 tag。请先 git tag -a ${TAG} -m '${TAG}' && git push origin ${TAG} && git push GitCode ${TAG}"
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

  # ── 6c. GitCode 上传（Gitee-compatible API v5） ──
  # API 文档（Gitee）: https://gitee.com/api/v5/swagger
  # GitCode 实现 Gitee v5 API 的子集：
  #   创建 release:  POST /repos/{owner}/{repo}/releases  (formData)
  #                  返回 release 对象包含 id 字段
  #   上传附件:      POST /repos/{owner}/{repo}/releases/{id}/attach_files  (multipart)
  # 认证: access_token 作为 formData 字段传
  # 注意: GitCode GET release 响应不包含 id 字段（与标准 Gitee 不同），
  #      但 POST 创建响应会返回完整对象含 id。若 release 已存在，
  #      建议在 GitCode 网页端先删除旧 release 再重新上传，或上传到新 tag。
  if [[ -n "${GITCODE_TOKEN:-}" ]]; then
    log "上传到 GitCode…"

    GCUA="Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 GoKYCH-ReleaseScript"

    # 先检查 release 是否已存在（GET 不带 id，但能确认存在性）
    GC_EXIST_CODE=$(curl -s -o /dev/null -w "%{http_code}" -H "User-Agent: $GCUA" \
      "${GC_API}/repos/${GC_OWNER}/${GC_REPO}/releases/tags/${TAG}" 2>/dev/null)

    if [[ "$GC_EXIST_CODE" == "200" ]]; then
      warn "GitCode release ${TAG} 已存在。GitCode GET API 不返回 release id，无法增量上传附件。"
      warn "请在 GitCode 网页端删除旧 release 后重新运行本脚本，或使用新 tag。"
      warn "（旧 release 页面: https://gitcode.com/${GC_OWNER}/${GC_REPO}/releases/tag/${TAG}）"
      # 继续尝试 POST 覆盖，万一 GitCode 支持 upsert
    fi

    log "创建 GitCode release ${TAG}…"
    # -F "body=<file" 让 curl 从文件读取 body 内容，避免 shell 转义问题
    GC_CREATE_BODY=$(curl -s -w "\n%{http_code}" -X POST \
      -H "User-Agent: $GCUA" \
      -F "access_token=${GITCODE_TOKEN}" \
      -F "tag_name=${TAG}" \
      -F "name=gokych ${TAG}" \
      -F "body=<${RELEASE_NOTES}" \
      -F "target_commitish=$(git -C "$REPO_ROOT" rev-parse HEAD)" \
      -F "prerelease=false" \
      "${GC_API}/repos/${GC_OWNER}/${GC_REPO}/releases" 2>&1) || true
    GC_CREATE_CODE=$(echo "$GC_CREATE_BODY" | tail -1)
    GC_CREATE_RESP=$(echo "$GC_CREATE_BODY" | sed '$d')
    GC_REL_ID=""

    if [[ "$GC_CREATE_CODE" == "201" || "$GC_CREATE_CODE" == "200" ]]; then
      GC_REL_ID=$(echo "$GC_CREATE_RESP" | python3 -c "import json,sys;print(json.load(sys.stdin).get('id',''))" 2>/dev/null || echo "")
      if [[ -n "$GC_REL_ID" ]]; then
        ok "GitCode release 创建成功 (id=${GC_REL_ID})，开始上传附件…"
      else
        warn "GitCode release 创建成功但响应中无 id，无法上传附件"
        warn "响应: $(echo "$GC_CREATE_RESP" | head -c 300)"
      fi
    else
      warn "GitCode 创建 release 失败 (HTTP ${GC_CREATE_CODE}): $(echo "$GC_CREATE_RESP" | head -c 300)"
    fi

    if [[ -n "$GC_REL_ID" ]]; then
      for f in "$DIST"/*; do
        fname="$(basename "$f")"
        printf "  上传 %s … " "$fname"
        UP_RESP=$(curl -s -w "\n%{http_code}" -X POST \
          -H "User-Agent: $GCUA" \
          -F "access_token=${GITCODE_TOKEN}" \
          -F "file=@${f}" \
          "${GC_API}/repos/${GC_OWNER}/${GC_REPO}/releases/${GC_REL_ID}/attach_files" 2>&1) || true
        UP_CODE=$(echo "$UP_RESP" | tail -1)
        UP_BODY=$(echo "$UP_RESP" | sed '$d')
        if [[ "$UP_CODE" == "201" || "$UP_CODE" == "200" ]]; then
          echo "✓"
        else
          echo "✗ (HTTP ${UP_CODE})"
          echo "    $(echo "$UP_BODY" | head -c 200)"
        fi
      done
      ok "GitCode 上传完成：https://gitcode.com/${GC_OWNER}/${GC_REPO}/releases/tag/${TAG}"
    fi
  else
    warn "GITCODE_TOKEN 未设置，跳过 GitCode 上传（设置: export GITCODE_TOKEN=你的私人令牌）"
  fi
fi

ok "全部完成 🚀"
echo
echo "产物在 ${DIST}/，把下面的文件作为 asset 上传到 GitHub/GitCode Release："
ls -1 "$DIST" | sed 's/^/  /'
