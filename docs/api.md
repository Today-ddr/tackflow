# Tack API v1

单用户任务看板的 HTTP 契约。所有接口以 `/api/v1` 为前缀；脚本和快捷指令使用 Bearer Token，网页使用 HttpOnly Session Cookie。

## 通用约定

- 时间使用 RFC3339 UTC；`due_at` 也接受 `YYYY-MM-DD`，按 `TACK_TZ` 的当天 23:59:59 解释。
- JSON 请求体最大 1 MiB；未知字段会被忽略。
- 响应带 `X-Request-Id`，错误体固定为：

```json
{
  "error": {
    "code": "task_not_found",
    "message": "任务不存在",
    "request_id": "req_..."
  }
}
```

- 分页参数：`limit` 默认 100，最大 500；`cursor` 是上一页返回的 `next_cursor`。任务按 `position, id` 排序。
- 变更请求可带 `Idempotency-Key`。同一 key 24 小时内返回首次成功响应，并带 `X-Tack-Idempotent-Replay: true`。
- 浏览器 Cookie 的变更请求必须带 `X-Tack-Client: web`，且 `Origin` 必须匹配 `TACK_PUBLIC_URL`；Bearer Token 不需要此头。

## 认证与权限

### Session Cookie

登录成功后设置：

- HTTP 开发环境：`tack_session`
- HTTPS 生产环境：`__Host-tack_session`，`HttpOnly; Secure; SameSite=Lax; Path=/`

Session 默认 30 天滑动续期。网页设置页可以列出和移除登录设备。

### API Token

Token 形如 `tk_...`，明文只在创建时返回一次，数据库只保存 SHA-256。Scope：

| scope | 能力 |
|---|---|
| `capture` | `POST /capture` |
| `read` | 读取任务、项目、标签、事件、导出 |
| `write` | 建卡、改卡、移动、评论、项目/标签写操作；同时隐含 `capture` 和 `read` |

Token 不能管理 Token、Session、账户密码或执行更新；这些操作只接受网页 Session。

### Sudo

以下操作要求最近 5 分钟内验证过密码：

- 新建 / 撤销 API Token
- 修改密码
- 归档项目
- 应用更新 / 回滚

先调用 `POST /auth/sudo`，或在相应接口的 `password` 字段中直接提供当前密码。Sudo 会写入当前 Session，后续 5 分钟内的请求不需要重复密码。

## 健康、版本与更新日志

| 方法 | 路径 | 认证 | 说明 |
|---|---|---|---|
| `GET` | `/healthz` | 无 | 进程存活，返回 `ok` |
| `GET` | `/readyz` | 无 | 检查 SQLite，返回 `ok` |
| `GET` | `/api/v1/version` | 无 | `{version, commit, build_time}` |
| `GET` | `/api/v1/changelog` | 无 | 内嵌 `CHANGELOG.md`，`text/markdown` |

## 登录与会话

### `POST /api/v1/auth/login`

```json
{"username":"admin","password":"..."}
```

成功返回 `200` 并设置 Session Cookie：

```json
{"user":{"username":"admin"}}
```

失败为 `401 invalid_credentials`；同 IP 每分钟超过 5 次失败返回 `429 rate_limited`。

### `GET /api/v1/auth/me`

```json
{
  "user": {"username": "admin"},
  "auth_kind": "session",
  "sudo_expires_at": "2026-11-16T09:05:00Z"
}
```

`auth_kind` 为 `session` 或 `token`；Token 请求的 `sudo_expires_at` 永远为 `null`。

### `POST /api/v1/auth/logout`

删除当前 Session，返回 `204`。

### `POST /api/v1/auth/sudo`

```json
{"password":"当前密码"}
```

成功返回 `200`：`{"sudo_expires_at":"..."}`。

### `POST /api/v1/auth/password`

```json
{"current_password":"旧密码","new_password":"至少 12 位的新密码"}
```

需要 Session 和 sudo；成功后撤销所有 Session、清除 Cookie，返回 `204`。

## 任务

任务对象：

```json
{
  "id": 1,
  "project_id": 2,
  "title": "修登录后的跳转",
  "notes": "补充说明",
  "status": "inbox",
  "priority": 2,
  "position": 1024,
  "due_at": "2026-11-17T15:59:59Z",
  "agent_ready": false,
  "created_at": "2026-11-16T09:00:00Z",
  "updated_at": "2026-11-16T09:00:00Z",
  "completed_at": null,
  "project": {"id":2,"name":"codeg","color":"clay","position":2048,"archived_at":null,"created_at":"...","task_count":3},
  "tags": [{"id":1,"name":"bug","color":"plum","created_at":"...","task_count":1}]
}
```

`status`：`inbox`、`todo`、`doing`、`review`、`done`；`priority`：0–3。

### `POST /api/v1/tasks`

```json
{
  "title": "写迁移",
  "notes": "",
  "status": "todo",
  "project_id": 2,
  "priority": 2,
  "position": 1536,
  "due_at": "2026-11-20",
  "agent_ready": false,
  "tags": ["后端"],
  "tag_ids": [1]
}
```

`title` 必填（最多 500 字），`tags` 中不存在的名字会自动创建。成功返回 `201` + 任务对象。

### `GET /api/v1/tasks`

查询参数：`status=inbox,todo`、`project=<id>`、`tag=<id>`、`q=`、`agent_ready=true|false`、`updated_since=<RFC3339>`、`limit`、`cursor`。

```json
{"tasks":[],"next_cursor":null}
```

### `GET /api/v1/tasks/{id}`

返回任务详情及时间线和外部关联：

```json
{"task":{},"events":[{"id":1,"kind":"created","payload":{"status":"inbox"},"created_at":"..."}],"links":[]}
```

### `PATCH /api/v1/tasks/{id}`

局部更新，可传：`title`、`notes`、`project_id`（`null` 表示取消项目）、`priority`、`due_at`（`null` 表示清空）、`agent_ready`、`tags`、`tag_ids`。返回更新后的任务。

### `POST /api/v1/tasks/{id}/move`

```json
{"status":"doing","position":1536}
```

`position` 可省略；省略时移到目标列末尾。进入 `done` 会写 `completed_at`，离开 `done` 会清空。

### `POST /api/v1/tasks/{id}/comments`

```json
{"text":"等验收"}
```

返回 `201` + `{id, kind:"commented", payload:{text}, created_at}`。

### `GET /api/v1/tasks/{id}/events`

返回 `{"events":[...]}`。

### `DELETE /api/v1/tasks/{id}`

软删除，返回 `204`。回收站保留 30 天。

## 快速收集

### `POST /api/v1/capture`

```json
{"text":"修登录跳转 #bug @codeg !p2 ^明天\n补充说明"}
```

第一行是标题，后续行进入 `notes`。语法：

- `#tag`：自动创建标签
- `@project`：必须已存在，否则 `422 unknown_project`
- `!p0`–`!p3`：优先级
- `^今天` / `^明天` / `^后天` / `^2026-11-20`：截止日，按 `TACK_TZ` 解释

返回 `201` + 任务对象（状态固定为 `inbox`）。

## 项目与标签

### 项目

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/projects` | `?include_archived=1` 可包含归档项目；返回 `{projects:[...]}` |
| `POST` | `/api/v1/projects` | `{name, color}`；颜色不传则按名称分配 |
| `PATCH` | `/api/v1/projects/{id}` | `{name?, color?, archived?}` |
| `DELETE` | `/api/v1/projects/{id}` | 需要 sudo；归档项目，返回 `204` |

颜色 token：`sage`、`clay`、`ink`、`sky`、`plum`。

### 标签

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/tags` | 返回 `{tags:[...]}` |
| `POST` | `/api/v1/tags` | `{name, color?}`；已存在返回 `409` |
| `DELETE` | `/api/v1/tags/{id}` | 解绑所有任务后删除标签 |

## Token 与设备

以下接口只接受网页 Session；新建和撤销需要 sudo。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/tokens` | 返回 `{tokens:[...]}`，不含明文和哈希 |
| `POST` | `/api/v1/tokens` | `{name, scopes, password?}`；返回一次明文和 `api_token` |
| `DELETE` | `/api/v1/tokens/{id}` | 撤销；可带 `{password?}` |
| `GET` | `/api/v1/sessions` | 当前用户未过期设备 |
| `DELETE` | `/api/v1/sessions/{id}` | 移除设备；删除当前设备会清 Cookie |

创建 Token 的示例：

```json
{"name":"iPhone 快捷指令","scopes":["capture","read"],"password":"当前密码"}
```

## 设置、更新与导出

### 设置

- `GET /api/v1/settings`：返回 `{update_interval_hours, timezone}`
- `PATCH /api/v1/settings`：`{"update_interval_hours": 6}`；允许 `0/6/12/24/48`

### 更新

`GET /api/v1/update/status` 返回：

```json
{
  "current_version": "0.2.0",
  "latest_version": "v0.2.1",
  "update_available": true,
  "interval_hours": 24,
  "last_checked_at": "...",
  "last_error": "",
  "release_body": "...",
  "release_url": "https://github.com/.../releases/tag/v0.2.1",
  "published_at": "...",
  "configured": true,
  "updater": {"current_tag":"v0.2.0","previous_tag":"","status":"success","message":"","updated_at":"...","backup_path":""}
}
```

- `POST /api/v1/update/check`：手动检查，限流 1 次/分钟；需要配置 `TACK_GITHUB_REPO`。
- `POST /api/v1/update/apply`：`{version, password?}`；版本必须高于当前版本，需要 sudo。
- `POST /api/v1/update/rollback`：`{password?}`；恢复上一版数据库和镜像，需要 sudo。

更新请求返回 `202` + `{"updater": {...}}`，实际进度通过状态接口或 updater 日志查看。

### 导出

`GET /api/v1/export?format=json|csv`，需要 `read` scope。JSON 包含项目、标签、任务；CSV 每个任务一行。

## curl 示例

```bash
BASE=http://localhost:8080

# 登录并保存 Cookie
curl -fsS -c /tmp/tack.cookies \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<password>"}' \
  "$BASE/api/v1/auth/login"

# 网页 Cookie 写请求
curl -fsS -b /tmp/tack.cookies \
  -H 'X-Tack-Client: web' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: capture-1' \
  -d '{"text":"修登录跳转 #bug @codeg !p2 ^明天"}' \
  "$BASE/api/v1/capture"

# Token 请求
curl -fsS \
  -H 'Authorization: Bearer tk_...' \
  -H 'Content-Type: application/json' \
  -d '{"text":"脚本记一笔"}' \
  "$BASE/api/v1/capture"
```

## 稳定错误码

`unauthorized`、`invalid_credentials`、`rate_limited`、`csrf_failed`、`web_only`、`sudo_required`、`invalid_password`、`insufficient_scope`、`invalid_idempotency_key`、`bad_request`、`invalid_status`、`invalid_priority`、`invalid_title`、`invalid_notes`、`invalid_due_at`、`invalid_project`、`invalid_tags`、`unknown_project`、`task_not_found`、`project_exists`、`tag_exists`、`tag_not_found`、`token_not_found`、`invalid_name`、`invalid_color`、`invalid_interval`、`update_not_configured`、`update_rate_limited`、`update_check_failed`、`update_unavailable`、`invalid_version`、`version_not_newer`、`invalid_format`、`internal_error`。
