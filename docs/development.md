# GoKYCH 本地开发 — 日常循环 + 常见坑

> 给"刚拉完代码,想跑起来 / 想 debug / 改完发现页面 500 之类"的人。
> 生产部署看 `docs/deployment.md`,代码层的计划/缺口看 `docs/TODO.md`。

## 1. 日常循环

```bash
# 后端(终端 A)— Go 不热重载,改完代码就重启
cd /Volumes/TiPro7000/Projects.localized/Go/GoKYCH
go run ./cmd/gokych          # 默认 :8000,读 .env

# 前端(终端 B)— Next.js 改文件自动 HMR
cd web
npx next dev -p 3000         # log → /private/tmp/gokych-web.log

# 自检(三件套,改完跑一下)
go test ./...                # Go 单测
npx tsc --noEmit             # web/ 端 TS 类型检查
npx next build               # web/ 端 prod build(发现 dev 模式掩盖的错)
```

数据库:本地 MySQL 8.0,`.env` 里 `DB_*` 配置。schema 自动从
`internal/core/schema/schema.go` migrate,启动时执行,**不要手动改表**。
管理员默认 `admin` / `admin123`(`ADMIN_USERNAME` / `ADMIN_PASSWORD`)。

## 2. 日志位置

| 进程               | 日志                                             |
| ------------------ | ------------------------------------------------ |
| 后端 (gokych)      | stdout / 你启动它的终端                          |
| 前端 (next dev)    | `/private/tmp/gokych-web.log`(后台模式)或终端   |
| MySQL              | 系统 log(macOS: `/usr/local/var/mysql/*.err`)    |
| typst CLI          | stdout,启动时 `typst CLI found path=...` 一行   |

## 3. 常见问题 + 修法

### 3.1 前端:`Cannot find module './vendor-chunks/xxx.js'`

**症状**:浏览器 500,dev log 报 `MODULE_NOT_FOUND` 一个 vendor chunk;
或者 `webpack-runtime.js` 引用的 chunk 在 `.next/server/vendor-chunks/`
里找不到。

**根因**:Next.js dev server 跑久了(几小时到几天),webpack 中间缓存
对不上源码(切 chunk 策略、import 拓扑变化、加新依赖等都会触发)。
`.next/` 在,但物理 chunk 缺了。

**修法**(三连):

```bash
pkill -f 'next dev'                                  # 杀旧 server
mavis-trash web/.next                                # 清缓存(macOS 回收站,不会真删)
cd web && nohup npx next dev -p 3000 > /private/tmp/gokych-web.log 2>&1 &
sleep 5
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:3000/   # 应该 200
```

> ⚠️ `mavis-trash` 不是 `rm`:文件进回收站,后悔可找回。

### 3.2 前端:TypeScript 类型对,但 dev 报红

Next.js dev 用的 SWC 转译不跑 `tsc`。dev 看着没事,prod build 才暴露。
`npx tsc --noEmit` + `npx next build` 是权威,dev 是**仅供参考**。

### 3.3 后端:改了 Go 代码不生效

Go 不热重载。**确认进程是新的**:

```bash
ps aux | grep gokych | grep -v grep
# 重启
pkill -TERM gokych   # 或 pkill -f 'go run ./cmd/gokych'
go run ./cmd/gokych &
```

### 3.4 typst 文章:显示"Typst 编译器未安装"

`internal/typst/typst.go` 找不到 `typst` CLI。装一下:

```bash
brew install typst       # macOS
# 或 https://github.com/typst/typst/releases 拿 prebuilt
```

`TYPST_PATH` 环境变量可指自定义路径,优先级高于 `$PATH`。

### 3.5 typst 文章:`#import "template.typ"` 失败

`template.typ` 由后端在 `SetWorkspaceDir` 时从 `embed.FS` 物化到
`data/typst/`(绝对路径 = `<DATA_DIR>/typst`,生产由 `main.go` 注入)。
本地开发默认相对 cwd,**在项目根跑** `go run ./cmd/gokych`,不要 `cd web` 再起后端。
验证:

```bash
ls data/typst/template.typ      # 应该存在
# 不存在就手动从 internal/typst/assets/template.typ 复制,或跑一遍后端让它物化
```

### 3.6 评论 / 通知里 markdown 不渲染

后端 `internal/content/parsers/markdown.go` 有两条:`RenderMarkdown`(unsafe,
给可信文章内容)和 `RenderSafeMarkdown`(XSS-safe,给用户输入)。前者会执行
raw HTML,后者直接剥掉。改 markdown 行为时确认走的是哪个。

### 3.7 部署 VM 上没装 Go / 想省掉 go build 时间

`deploy-backend.sh` 默认会本地 `go build`,VM 上得有 Go 工具链。VM 上
已经用 `install-backend.sh` 装过 gokych 的话,加 `--use-installed`
直接复用装好的二进制,VM 不需要 Go:

```bash
# 1. 一次性装二进制(以后更新二进制也用它)
curl -fsSL https://raw.githubusercontent.com/CrossDark/GoKYCH/main/scripts/install-backend.sh | sudo bash

# 2. 跑 deploy(本机模式,无 SSH,无 go build)
sudo --use-installed bash scripts/deploy-backend.sh

# 3. 后续只更新二进制 → install-backend.sh
# 4. 后续只更新 systemd / nginx / certbot → --use-installed + --update
sudo --use-installed bash scripts/deploy-backend.sh --update
```

不传 `--use-installed` 时脚本会探测 `command -v gokych`,如果找到
就 warn 提示你"加 --use-installed 可省掉 go build"(行为不变,
go build 仍跑)。

适用场景:
- 轻量 VM / 容器镜像不要 Go(省 ~300MB)
- CI 流水线已经 prebuild 二进制,deploy 脚本只做 systemd / nginx
- 频繁重 deploy 想省 30+ 秒的 go build

详细的环境变量和触发条件见 `docs/deployment.md` §"跑在哪？"。

## 4. 兜底:全量重置

数据库不动,只重置应用层状态:

```bash
# 1. 杀进程
pkill -f 'next dev'   2>/dev/null
pkill -f gokych       2>/dev/null

# 2. 清构建缓存(保留 node_modules)
mavis-trash web/.next web/node_modules/.cache   2>/dev/null

# 3. 验证依赖没漂
cd web && npm install
go mod tidy

# 4. 重启
go run ./cmd/gokych &
cd web && nohup npx next dev -p 3000 > /private/tmp/gokych-web.log 2>&1 &
```

数据库真要重置(慎):`mavis-trash data/uploads/* && DROP DATABASE gokych;
CREATE DATABASE gokych;` 然后重启 gokych,schema 自动重建。
**会丢所有文章 / 评论 / 通知 / 用户**,只适用于本地反复重来的场景。
