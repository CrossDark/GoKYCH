# API 接口文档

所有 API 均以 `/api` 为前缀，返回 JSON 格式。

## 通用说明

- **认证方式**：基于 Cookie 的 Session
- **CSRF 保护**：写操作（POST/PUT/DELETE）需携带 CSRF Token
  - 先 `GET /api/auth/csrf` 获取 Token
  - 请求头带 `X-CSRF-Token: <token>`
- **权限角色**：`user`（普通用户）、`admin`（管理员）、`owner`（拥有者）

---

## 公共接口

### 健康检查

```
GET /api/health
```

响应：`{"status": "ok"}`

### 站点信息

```
GET /api/site
```

### 首页数据

```
GET /api/home
```

### 通知列表

```
GET /api/notifications
```

---

## 文章

### 文章列表

```
GET /api/articles?type={type}&page={page}&per_page={per_page}
```

| 参数 | 说明 |
|------|------|
| `type` | 文章类型：`md` / `wikidot` / `html` / `bbcode` / `typst` |
| `page` | 页码，默认 1 |
| `per_page` | 每页数量，默认 20 |

### 文章详情

```
GET /api/articles/:type/:slug
```

### 全文搜索

```
GET /api/search?q={keyword}&page={page}
```

---

## 评论

### 获取评论

```
GET /api/articles/:type/:slug/comments
```

### 获取行评论

```
GET /api/articles/:type/:slug/line-comments
```

### 发表评论（需登录 + CSRF）

```
POST /api/articles/:type/:slug/comments
```

### 发表行评论（需登录 + CSRF）

```
POST /api/articles/:type/:slug/line-comments
```

---

## 评分

### 获取文章评分

```
GET /api/articles/:type/:slug/rating
```

### 获取评分详情列表

```
GET /api/articles/:type/:slug/ratings
```

### 设置评分（需登录 + CSRF）

```
POST /api/articles/:type/:slug/rating
```

Body：`{"score": 0.8}` （范围 -1.00 ~ 1.00）

### 删除评分（需登录 + CSRF）

```
DELETE /api/articles/:type/:slug/rating
```

---

## 认证

### 获取当前用户

```
GET /api/auth/me
```

### 获取 CSRF Token

```
GET /api/auth/csrf
```

### 登录（需 CSRF）

```
POST /api/auth/login
```

Body：`{"username": "...", "password": "..."}`

### 登出（需 CSRF）

```
POST /api/auth/logout
```

---

## 管理接口（需 admin 角色 + CSRF）

### 文章管理

```
POST   /api/articles              — 创建文章
PUT    /api/articles/:type/:slug  — 更新文章
DELETE /api/articles/:type/:slug  — 删除文章
```

### 用户管理

```
GET    /api/admin/users              — 用户列表
POST   /api/admin/users              — 创建用户（需 admin）
PUT    /api/admin/users/:username/role — 修改角色（需 owner）
DELETE /api/admin/users/:username     — 删除用户（需 owner）
```

### 通知管理

```
GET    /api/admin/notifications       — 通知列表
POST   /api/admin/notifications       — 创建通知
PUT    /api/admin/notifications/:id   — 更新通知
DELETE /api/admin/notifications/:id   — 删除通知
```

### 站点设置

```
GET /api/admin/settings   — 获取设置（需 admin）
PUT /api/admin/settings   — 更新设置（需 owner）
```

### 首页管理

```
GET  /api/admin/home             — 获取首页管理数据
POST /api/admin/home/links       — 添加子站链接
DELETE /api/admin/home/links/:id — 删除子站链接
POST /api/admin/home/featured       — 添加精选文章
DELETE /api/admin/home/featured/:id — 删除精选文章
```

### 文件管理

```
GET /api/admin/files — 文件列表
```

### 个人资料

```
GET /api/admin/profile  — 获取个人资料
PUT /api/admin/profile  — 更新个人资料
```

---

## 标签

### 标签列表

```
GET /api/labels
```

### 标签下的文章

```
GET /api/labels/:tag
```
