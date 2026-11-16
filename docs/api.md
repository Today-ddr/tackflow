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
- 支持幂等的变更请求可带 `Idempotency-Key`。缓存按 Token/Session、HTTP 方法、路径和 key 隔离，24 小时内返回首次成功响应，并带 `X-Tack-Idempotent-Replay: true`。同一路径重用 key 时，修改请求体也不会再次执行；不同操作应使用新 key。认证挑战不缓存。
- 普通变更在执行前持久登记 key；若中断或结果无法落盘，相同 key 返回 `409 idempotency_uncertain`，需先读取现状再决定后续操作，不能用新 key 盲目重试。Agent 领取的结果与任务变更在同一事务中保存，可原子恢复。缓存内容加密，依赖原 `TACK_SESSION_SECRET`；更换该密钥会使旧缓存不可读。
- 浏览器 Cookie 的变更请求必须带 `X-Tack-Client: web`，且 `Origin` 必须匹配 `TACK_PUBLIC_URL`；Bearer Token 不需要此头。

## 认证与权限

### Session Cookie

登录成功后设置：

- HTTP 开发环境：`tack_session`
- HTTPS 生产环境：`__Host-tack_session`，`HttpOnly; Secure; SameSite=Lax; Path=/`

Session 默认 30 天滑动续期。网页设置页可以列出和移除登录设备。

### API Token

Token 形如 `tk_...`，普通列表不返回明文，凭证表只保存 SHA-256。创建响应的幂等缓存加密保存；取回该响应仍需当前 sudo，不会再次创建 Token。Scope：

| scope | 能力 |
|---|---|
| `capture` | `POST /capture` |
| `read` | 读取任务、项目、标签、事件、导出 |
| `write` | 建卡、改卡、移动、评论、项目/标签写操作；同时隐含 `capture` 和 `read` |

Token 不能管理 Token、Session、Passkey、Webhook、回收站、账户密码或执行更新；这些操作只接受网页 Session。`read` 的事件权限仅指单任务时间线；实时 SSE 也仅接受 Session。

创建 Token 可传 `project_ids` 和 `tag_ids` 数字数组；省略或空数组表示该维度不限制。每个维度内为 OR，两个维度之间为 AND。例如 `project_ids:[1,2]`、`tag_ids:[3]` 允许项目 1 或 2 中带标签 3 的任务。旧 Token 迁移后仍不限制资源。

限制覆盖任务列表、详情、事件、评论、移动、删除、capture、导出和 agent 领取。范围外任务返回 `404`；建卡或修改后的归属不满足限制返回 `403 resource_forbidden`。受限 Token 只能使用已有标签，不能创建或修改全局项目/标签；关联名称和数量只呈现可见任务或明确授权的分类。幂等重放前重新检查任务范围。每个数组最多 100 项，创建时必须为有效 ID。

删除标签会同时撤销范围中引用该标签的 Token，避免标签 ID 重用后扩大旧凭证的权限；需要继续使用时重新创建 Token。任务 ID 使用持久单调序列，回收站清理后不复用。

创建 Token 可选 `expires_at`：省略或 `null` 表示不过期，其他值必须为未来 365 天内的 RFC3339 时间。服务端归一为 UTC 秒精度；已到期的 Token 对所有认证接口返回 `401 unauthorized`，进入写锁后再次复核。旧 Token 无默认到期时间。管理列表保留已过期但未撤销的 Token，供查看和撤销。

### 连接检查

`GET /api/v1/agent/connection` 接受任意有效 Bearer Token（包括仅 `capture`）或网页 Session，无任务写入，响应 `Cache-Control: no-store`：

```json
{
  "api_base_url": "https://tack.example.com/api/v1",
  "auth_kind": "token",
  "transport": "rest",
  "mcp_supported": false,
  "token": {"id":1,"name":"只读助手","prefix":"tk_example","scopes":["read"],"project_ids":[2],"tag_ids":[],"created_at":"2026-11-17T00:00:00Z","last_used_at":null,"expires_at":"2026-12-17T00:00:00Z"}
}
```

Session 响应为 `auth_kind:"session", token:null`，不能据此判断 Key 可用。API 地址来自 `TACK_PUBLIC_URL`，不信任 Host/转发头。只返回当前凭证元数据，不包含 hash、明文、其他 Token 或任务。此请求会更新 Token 最近使用时间，不能证明 Agent 在线或远程网络可达。

### Sudo

以下操作要求最近 5 分钟内验证过密码：

- 新建 / 撤销 API Token
- 修改密码
- 归档项目
- 注册 / 删除 Passkey
- 新建 / 修改 / 删除 Webhook
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

`archived:true` 和 `archived:false` 都需要 Session + sudo，分别归档和恢复项目；任务不会被删除。

### 标签

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/tags` | 返回 `{tags:[...]}` |
| `POST` | `/api/v1/tags` | `{name, color?}`；已存在返回 `409` |
| `PATCH` | `/api/v1/tags/{id}` | `{name?, color?}`；重名返回 `409` |
| `DELETE` | `/api/v1/tags/{id}` | 解绑所有任务后删除标签 |

## Token 与设备

以下接口只接受网页 Session；新建和撤销需要 sudo。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/tokens` | 返回 `{tokens:[...]}`，不含明文和哈希 |
| `POST` | `/api/v1/tokens` | `{name, scopes, project_ids?, tag_ids?, expires_at?, password?}`；返回一次明文和 `api_token` |
| `DELETE` | `/api/v1/tokens/{id}` | 撤销；可带 `{password?}` |
| `GET` | `/api/v1/sessions` | 当前用户未过期设备 |
| `DELETE` | `/api/v1/sessions/{id}` | 移除设备；删除当前设备会清 Cookie |

创建 Token 的示例：

```json
{"name":"iPhone 快捷指令","scopes":["capture","read"],"password":"当前密码"}
```

`api_token` 和列表元素包含 `id/name/prefix/scopes/project_ids/tag_ids/created_at/last_used_at/expires_at`。到期参数非法返回 `422 invalid_expiry`。网页入口是 `/settings/connections`（`/settings/tokens` 仍可用），详细 Agent 接入步骤见 [Agent 连接](agent-connections.md)。

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

## 回收站

- `GET /api/v1/trash?limit=100&cursor=...`：仅 Session，返回 `{tasks:[...],next_cursor}`；每个任务包含 `deleted_at`。
- `POST /api/v1/tasks/{id}/restore`：仅 Session，支持幂等，恢复删除未满 30 天的任务并返回任务对象。保留原状态、项目和仍存在的标签；过期或不存在返回 `404`。
- 每日维护物理清理过期任务；恢复窗口按删除时间判断，不依赖维护是否已经执行。

## Agent 领取与结果

| 方法 | 路径 | scope | 说明 |
|---|---|---|---|
| `GET` | `/api/v1/agent/next` | `read` | 只读预览下一个 `todo` 且 `agent_ready` 的任务，返回 `{task:null或任务}` |
| `POST` | `/api/v1/agent/claim` | `write` | `{worker:"worker-name"}`，原子领取并移到 `doing` |
| `POST` | `/api/v1/agent/tasks/{id}/heartbeat` | `write` | `{claim_id:"..."}`，续租 30 分钟 |
| `POST` | `/api/v1/agent/tasks/{id}/result` | `write` | 提交结果、评论和外部关联 |

领取返回 `{task,claim:{id,expires_at}}`；没有待办时二者均为 `null`。claim id 是临时凭证，领取记录保存哈希，绑定领取的 Token 或 Session；可重放响应加密保存，并与领取在同一事务提交。重放必须仍满足当前资源权限和有效租约，旧 key 过期返回 `409`，不会分配新任务。`GET /agent/next?claim=1` 被拒绝，GET 不产生领取副作用。

```json
{"claim_id":"...","status":"review","comment":"已完成实现，等待人工验收","links":[{"kind":"codeg","external_id":"work-task-17","url":"codeg://session/555"}]}
```

`status` 可省略，默认 `review`；仅允许 `review`、`done`、`todo`，前两者会关闭 `agent_ready`。评论、关联和状态在同一事务内落库，重复结果不会追加重复评论。过期、被人工修改或其他 worker 的领取返回 `409`；不允许旧 worker 覆盖后续人工操作。外部链接仅接受 `http`、`https`、`codeg` 协议。领取续租前请保存 claim id，勿写入日志。

## Passkey

生产需要 HTTPS，localhost 允许本地测试。RP ID 和允许的 Origin 由 `TACK_PUBLIC_URL` 决定，不信任请求 Host。密码登录保留。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/v1/passkeys` | Session，`{passkeys:[{id,name,created_at,last_used_at}]}` |
| `POST` | `/api/v1/passkeys/register/begin` | Session + sudo，`{name,password?}` → `{challenge_id,publicKey}` |
| `POST` | `/api/v1/passkeys/register/finish` | Session，`{challenge_id,credential}` → `201 {passkey}` |
| `DELETE` | `/api/v1/passkeys/{id}` | Session + sudo，`{password?}` → `204` |
| `POST` | `/api/v1/auth/passkey/begin` | 登录挑战 → `{challenge_id,publicKey}`，设置临时 HttpOnly 浏览器绑定 Cookie |
| `POST` | `/api/v1/auth/passkey/finish` | `{challenge_id,credential}` → 普通 Session Cookie 和 `{user:{username}}` |

`publicKey` 为标准 WebAuthn JSON，二进制字段为 base64url；客户端转换为 ArrayBuffer 后调用 `navigator.credentials.create/get`，将结果序列化为标准 `credential` JSON。挑战有效 5 分钟、单次消费，绑定用途和原会话/登录浏览器；要求用户验证并校验签名、Origin、RP ID 和计数器。取消系统认证框时可重试或继续使用密码。

## 实时事件

`GET /api/v1/events` 仅 Session，返回 `text/event-stream`，不接受 URL 中的 Token。首次连接从最新序号开始，收到 `ready` 事件；`change` 的 `id` 是持久化事件序号，data 形如：

```json
{"id":42,"resource":"task","resource_id":3,"kind":"updated","created_at":"2026-11-16T09:00:00Z"}
```

使用 `Last-Event-ID` 或首次连接的 `?cursor=42` 回放之后的事件，15 秒心跳并复核 Session 是否撤销。事件只表达资源变更；客户端重新读取原接口得到权威状态。

## Webhook

管理仅 Session，变更需要 sudo：

| 方法 | 路径 | 请求 / 返回 |
|---|---|---|
| `GET` | `/api/v1/webhooks` | `{webhooks:[...]}`，不含 secret |
| `POST` | `/api/v1/webhooks` | `{name,url,events:["task.*"],password?}` → `201 {webhook,secret}`，secret 只返回一次 |
| `PATCH` | `/api/v1/webhooks/{id}` | `{name?,url?,events?,enabled?,password?}` |
| `DELETE` | `/api/v1/webhooks/{id}` | `{password?}` → `204` |
| `GET` | `/api/v1/webhooks/{id}/deliveries` | `{deliveries:[{id,event_id,status,attempts,next_attempt_at,last_error,delivered_at}]}` |

订阅支持 `task.*`、`project.*`、`tag.*`、`*`。投递 JSON 为 `{id,event,created_at,data:{resource,resource_id,kind}}`，不包含任务正文或认证数据。按事件 ID 去重；这是至少一次投递，服务重启和响应丢失可能重试。

请求头 `X-Tack-Event`、`X-Tack-Delivery`、`X-Tack-Timestamp`、`X-Tack-Signature`。签名为 `sha256=` 加 HMAC-SHA256 十六进制，签名输入是时间戳 + `.` + 原始请求体。接收端应恒定时间验签、限制时间戳偏差并记录 delivery ID 防重放。

每次超时 5 秒，最多 5 次指数退避重试，投递状态持久化。禁用或删除后停止待投递。默认仅 HTTPS，拒绝 URL 凭证、重定向、私有/回环/链路本地 IP 和 DNS 答案；每次拨号重新解析并固定已验证地址。开发时显式 `TACK_WEBHOOK_ALLOW_PRIVATE=true` 才允许本地 HTTP 接收器。

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
