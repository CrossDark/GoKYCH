# GoKYCH Wiki

欢迎来到 GoKYCH 项目 Wiki。

GoKYCH 是一个支持多种标记语言（Markdown、Wikidot、HTML、BBCode、Typst）的内容管理系统，采用 Go + Next.js 前后端分离架构。

## 目录

- [[架构概览]] — 项目整体架构、技术栈与目录结构
- [[API-接口文档]] — 后端 RESTful API 接口说明
- [[数据库设计]] — 数据库表结构与关系
- [[配置说明]] — 环境变量、YAML 配置与优先级
- [[部署指南]] — Docker 部署与本地开发部署
- [[开发指南]] — 本地开发环境搭建与开发规范

## 快速开始

```bash
# 1. 复制环境变量
cp .env.example .env

# 2. 启动 Docker（MySQL + 后端）
docker compose up -d

# 3. 启动前端开发服务器
cd web && npm install && npm run dev
```

访问：
- 后端 API：`http://localhost:8000`
- 前端页面：`http://localhost:3000`
- 默认管理员：`admin` / `admin123`
