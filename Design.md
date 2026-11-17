# Tack 设计文档

> 项目名：**Tack**（图钉）· 中文叫「图钉」
> 状态：M1–M4 已实现 · v0.2.0 · 2026-11-16
> 仓库：`github.com/Today-ddr/tackflow` · Go 模块：`github.com/today-ddr/tackflow`
> 一句话：把念头钉在板上，让脚本和 agent 也能看见。

---

## 1. 背景

现在的任务都散在 Codeg 的 work_task 板和脑子里。Codeg 适合“真的要开一个 agent 干活”的场景，但打开它成本太高，地铁上想到一句话、开会时记一个待办，都不该走这条路径。

需要一块自己的板：

- 手机上 3 秒内记一条，不用打开 Codeg；
- 桌面上能拖着看，知道现在在做什么；
- 脚本和 agent 能通过 API 建卡、取卡、改状态；
- 数据在自己服务器上，单容器部署，坏了能自己修。

同时这是一个长期用 AI 迭代的个人项目，所以规范、文档和测试要和代码一起长出来，而不是事后补。

## 2. 目标

1. **快**：从点开图标到关掉，记一条不超过 5 秒。
2. **一块板**：收件箱 + 看板，碎碎念和项目任务共用一个池子；项目是彩色分类，不是多块板。
3. **API 优先**：界面上能做的事，API 都能做；token 可撤销、可按 scope 限权。
4. **单容器**：Go 单二进制 + SQLite，`docker compose up -d` 完事。
5. **网页内更新**：检查新版本、看更新日志、点一下升级、失败自动回滚。
6. **可长期维护**：AI 能读懂仓库、跑通测试、按规范提交；人只需要点头。

## 3. 非目标

- 多人协作、组织、权限体系（单用户，最多以后加一个只读分享链接）。
- 原生 iOS / Android App（PWA + 快捷指令足够）。
- CalDAV / 日历双向同步（v1 不做）。
- 甘特图、表格视图、时间追踪（v1 不做）。
- 富文本编辑器（Markdown 子集：加粗、代码、链接、列表）。
- 通用 webhook 平台（v1 只保留内部事件表，v1.1 再开 webhook）。

## 4. 反目标

- 不要变成一个需要看运维手册才能跑起来的系统。
- 不要为了“功能全”牺牲记录速度。
- 不要让 UI 花哨到影响扫读。
- 不要把所有碎碎念自动送进 agent 执行。

## 5. 使用场景

**A. 手机随手记**
主屏「Tack」图标 → 全屏输入框（自动聚焦）→ 打字 → 回车 → 存进 Inbox。支持 `#标签`、`@项目`、`!p2`、`^明天` 内联语法。

**B. 晚上整理**
打开看板 → Inbox 里的条目拖到 To do / Doing，或直接归档。

**C. 脚本写入**
日报脚本、CI 失败通知、RSS 摘要，POST 一段文字到 `/api/v1/capture`，落在 Inbox。

**D. agent 取活**
Codeg 的 worker 轮询 `agent_ready = true` 的 todo 卡，领走执行，回写状态和评论。碎碎念默认不带这个标记。

## 6. 方案选择

### 甲：直接用 Vikunja

成熟、现成、功能全。但它不是“我的项目”，手机端也只能用它的界面；而且和 Codeg 的对接仍要另写 worker。

### 乙：Vikunja 做后端 + 自写前端

账号、权限、SQLite、webhook 全部白拿，只写 UI。代价是两套系统、一层字段映射，还要跟着 Vikunja 的 API 演进走。

### 丙：自建，参考 Vikunja 的数据模型（选中）

一个 Go 二进制 + SQLite + 自己定义 API。参考 Vikunja 已经把问题想清楚的部分（project / task / bucket / label 的拆分、scoped token、webhook 事件名），但不继承它的代码和历史。

**选择理由**：目标是长期维护的个人项目，代码量和部署复杂度要可控；真正值钱的是设计约定，不是 159MB 的仓库。

## 7. 总体架构

```
┌──────────────┐   HTTPS    ┌──────────────────────┐
│ 浏览器 / PWA  │──────────▶│ Caddy（自动 TLS）     │
│ iOS 快捷指令  │           └──────────┬───────────┘
└──────────────┘                      │
                                      ▼
                           ┌──────────────────────┐
                           │ app（Go 单二进制）    │
                           │  · 内嵌前端静态文件    │
                           │  · REST API           │
                           │  · SQLite /data       │
                           └───────┬──────┬───────┘
                                   │      │ control 网络
                        ┌──────────▼──┐   ▼
                        │ /data/*.db  │ ┌─────────────────┐
                        └─────────────┘ │ updater sidecar │
                                        │  docker.sock    │
                                        └────────┬────────┘
                                                 │ pull / recreate
                                                 ▼
                                        ┌─────────────────┐
                                        │ GHCR 镜像        │
                                        └─────────────────┘
```

- 公网只暴露 Caddy 的 80/443。
- `app` 不直接接触 docker socket。
- `updater` 只在 `control` 私有网络里，不发布端口，靠共享 token 认 app。
- 版本检查由 `app` 调 GitHub Releases API；镜像拉取由 `updater` 调 docker。

## 8. 技术选型

| 层 | 选择 | 理由 |
|---|---|---|
| 后端 | Go（最新稳定版，go.mod 锁定 minor） | 单二进制、静态编译、部署最简单 |
| HTTP | 标准库 `net/http` + `ServeMux` | 路由量小，不引框架 |
| DB | SQLite（`modernc.org/sqlite`，纯 Go） | 无 cgo，交叉编译无痛 |
| 迁移 | 内嵌 SQL + `schema_migrations` 表 | 约 80 行，AI 容易理解 |
| 前端 | React + Vite + TypeScript strict | 生态、组件库、拖拽都成熟 |
| 样式 | Tailwind v4 + CSS 变量 token | 换主题只动 token |
| 组件 | shadcn/ui（复制进仓库） | 源码可改，不被库的审美绑住 |
| 拖拽 | dnd-kit | 键盘可达、移动端可用 |
| 数据请求 | TanStack Query | 缓存、乐观更新、重试都现成 |
| 其他 | cmdk / vaul / sonner / motion | 命令面板、移动抽屉、提示、过渡 |
| 测试 | `go test` + Vitest + Testing Library | 后端契约 + 前端逻辑 |
| 构建 | 多阶段 Dockerfile → distroless | 最终 25–35MB，非 root |
| CI | GitHub Actions | 构建、多架构、GHCR、Release |
| Agent skill | 现有 4 个 skill 源（见 §18.10） | 视觉、动效、手机端、边角数据 |

不引入 ORM、不引入 Redis、不引入消息队列、不引入需要额外容器的运行时依赖。

## 9. 数据模型

### 9.1 表

```sql
-- 单用户：只有 id = 1 这一行
CREATE TABLE user (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  username      TEXT NOT NULL,
  password_hash TEXT NOT NULL,          -- argon2id PHC 字符串
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE session (
  id           TEXT PRIMARY KEY,        -- sha256(random 32 bytes)
  user_id      INTEGER NOT NULL REFERENCES user(id),
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  user_agent   TEXT,
  ip           TEXT
);

CREATE TABLE api_token (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,           -- tk_xxxxxxxx，用于列表展示
  hash         TEXT NOT NULL UNIQUE,    -- sha256(完整 token)
  scopes       TEXT NOT NULL,           -- JSON array: capture/read/write
  created_at   TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at   TEXT
);

CREATE TABLE project (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  color       TEXT NOT NULL,            -- token 名，不是 hex
  position    REAL NOT NULL,
  archived_at TEXT,
  created_at  TEXT NOT NULL
);

CREATE TABLE task (
  id           INTEGER PRIMARY KEY,
  project_id   INTEGER REFERENCES project(id),
  title        TEXT NOT NULL,
  notes        TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL,           -- inbox|todo|doing|review|done
  priority     INTEGER NOT NULL DEFAULT 0, -- 0 none, 1 low, 2 med, 3 high
  position     REAL NOT NULL,
  due_at       TEXT,
  agent_ready  INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  completed_at TEXT,
  deleted_at   TEXT
);

CREATE INDEX task_board_idx ON task(deleted_at, status, position);
CREATE INDEX task_project_idx ON task(project_id, status);
CREATE INDEX task_updated_idx ON task(updated_at);

CREATE TABLE tag (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  color      TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE task_tag (
  task_id INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, tag_id)
);

CREATE TABLE task_event (
  id         INTEGER PRIMARY KEY,
  task_id    INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,             -- created|moved|updated|commented|linked
  payload    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

-- 外部系统关联：Codeg work_task、GitHub issue 等
CREATE TABLE task_link (
  id          INTEGER PRIMARY KEY,
  task_id     INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,            -- codeg_work_task
  external_id TEXT NOT NULL,
  url         TEXT,
  created_at  TEXT NOT NULL,
  UNIQUE (kind, external_id)
);

CREATE TABLE setting (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE idempotency (
  key         TEXT PRIMARY KEY,
  status      INTEGER NOT NULL,
  body        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
```

### 9.2 约定

- 所有时间存 UTC，RFC3339 字符串。
- 软删除用 `deleted_at`，列表默认过滤。
- `position` 用 REAL，插值排序；相邻差小于 `1e-9` 时整列重排。
- `status` 用 CHECK 约束固定五个值；新增状态必须走迁移。
- `project` 是单板上的彩色分类（类似标签的强类型版），不是多块看板。
- 颜色只存 token 名（`sage` / `clay` / `ink` / `sky` / `plum`），前端映射成实际色值。

## 10. 状态语义

| 状态 | 含义 | 看板可见 |
|---|---|---|
| `inbox` | 刚记下的念头，未分类 | 收件箱栏 |
| `todo` | 决定要做 | 是 |
| `doing` | 正在做 | 是 |
| `review` | 等 agent / 等人验收 | 有卡时才显示 |
| `done` | 完成 | 是（默认折叠归档） |

- 拖到 `done` 写 `completed_at`；拖出 `done` 清空。
- 进入 `doing` 记一条 `task_event`。
- `agent_ready` 默认 false；只有手动打开或 API 显式设置才为 true。
- 删除是软删除；回收站保留 30 天，之后由每日任务物理清理。

## 11. API 设计

### 11.1 通用

- 前缀：`/api/v1`
- 认证：浏览器用 `__Host-tack_session` Cookie；脚本用 `Authorization: Bearer tk_...`
- 内容类型：`application/json; charset=utf-8`
- 时间：RFC3339 UTC
- 错误体：

```json
{
  "error": {
    "code": "task_not_found",
    "message": "任务不存在",
    "request_id": "req_01J..."
  }
}
```

- 每个响应带 `X-Request-Id`；日志同一份 id。
- 变更类请求支持 `Idempotency-Key`，按 Token/Session、方法、路径隔离；24 小时内重复 key 返回首次成功结果。受限任务重放前重新验证资源权限。
- 分页用 cursor：`?limit=100&cursor=...`，响应带 `next_cursor`。

### 11.2 路由

**任务**

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/tasks` | 建卡 |
| `GET` | `/tasks` | 列表：`status`、`project`、`tag`、`q`、`agent_ready`、`updated_since`、`limit`、`cursor` |
| `GET` | `/tasks/{id}` | 详情（含标签、事件、关联） |
| `PATCH` | `/tasks/{id}` | 局部更新（标题、备注、项目、优先级、截止、agent_ready） |
| `DELETE` | `/tasks/{id}` | 软删除 |
| `POST` | `/tasks/{id}/move` | `{status, position}`，拖拽和状态流转都走这里 |
| `POST` | `/tasks/{id}/comments` | 追加一条 `task_event` |
| `GET` | `/tasks/{id}/events` | 时间线 |

**快速收集**

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/capture` | `{text}`，解析 `#标签`、`@项目`、`!p0..p3`、`^日期`，返回 201 + task |

**元数据**

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET/POST` | `/projects` | 项目列表 / 新建 |
| `PATCH/DELETE` | `/projects/{id}` | 改名、配色、归档 |
| `GET/POST` | `/tags` | 标签 |
| `PATCH/DELETE` | `/tags/{id}` | 改名配色 / 删除标签（只解绑，不删卡） |

**认证**

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/auth/login` | `{username, password}` → 设置 Cookie |
| `POST` | `/auth/logout` | 撤销当前 session |
| `POST` | `/auth/password` | 改密码（需要 sudo） |
| `GET` | `/auth/me` | 当前用户 + sudo 过期时间 |
| `GET/POST` | `/tokens` | 列出 / 新建 API token（明文只返回一次） |
| `DELETE` | `/tokens/{id}` | 撤销 |
| `GET` | `/sessions` | 登录设备列表 |
| `DELETE` | `/sessions/{id}` | 踢掉某个设备 |

**系统**

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/version` | `{version, commit, build_time}`，无需认证 |
| `GET` | `/changelog` | 内嵌 `CHANGELOG.md`，无需认证 |
| `GET` | `/update/status` | 当前/最新版本、上次检查时间、检查频率、更新任务状态 |
| `POST` | `/update/check` | 手动检查（限流 1 次/分钟） |
| `POST` | `/update/apply` | `{version, password}`，需要 sudo，转给 updater |
| `POST` | `/update/rollback` | 回滚到上一版本，需要 sudo |

**Agent（M5）**

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/agent/next` | 只读预览一张 `agent_ready` 的 todo 卡，拒绝 `?claim=1` |
| `POST` | `/agent/claim` | 原子领取，移到 doing，返回 claim id 和 30 分钟租约 |
| `POST` | `/agent/tasks/{id}/heartbeat` | 验证领取主体和 claim id，续租 |
| `POST` | `/agent/tasks/{id}/result` | 同事务写结果、评论、外链和状态，默认 review |

### 11.3 capture 语法

```
修一下登录后的跳转 #bug @codeg !p2 ^明天
```

- `#tag` → 标签（不存在则创建，颜色按哈希分配）
- `@project` → 项目（不存在则报 `unknown_project`，不自动建）
- `!p0`–`!p3` → 优先级
- `^今天`、`^明天`、`^2026-11-20` → 截止日期
- 其余作为标题；换行后的部分作为备注

## 12. 认证与安全

### 12.1 结论

**密码 + Session Cookie + API Token + Passkey（WebAuthn）。**

不接微信登录：个人主体申请网站应用需要企业资质和备案域名，审批周期长，PWA 里跳转体验也差。

不接 Sign in with Apple：需要 Apple Developer Program（$99/年）、Service ID、域名验证、client secret 定期轮换，收益不匹配。

Bitwarden 不是 IdP，没法给第三方站点做 OAuth；但它可以存密码和 **Passkey**。快速鉴权通过 WebAuthn 完成，密钥由 iCloud 钥匙串或 Bitwarden 等认证器保存。服务端使用 go-webauthn 验证签名，要求用户验证；挑战绑定用途、会话或登录浏览器，5 分钟内单次使用。密码登录始终保留。

### 12.2 密码

- argon2id：`time=3, memory=64MiB, threads=2, keyLen=32`，存 PHC 字符串。
- 首次启动从 `TACK_ADMIN_PASSWORD` 创建用户；之后该环境变量被忽略。
- 密码最短 12 位；不设复杂度规则，但拒绝常见弱密码表里的值。
- 登录失败：同 IP 5 次/分钟 → 429；同账号连续 10 次失败 → 锁 15 分钟。

### 12.3 Session

- 32 字节随机数，Cookie 里存原文，DB 里存 SHA-256。
- `__Host-tack_session`：`HttpOnly; Secure; SameSite=Lax; Path=/`。
- 有效期 30 天，滑动续期；一次登录一条记录，可在设置里踢掉。
- 修改密码或撤销 token 时可选择“踢掉所有设备”。

### 12.4 CSRF

- `SameSite=Lax` + 所有变更请求校验 `Origin`/`Sec-Fetch-Site`。
- 前端所有请求带 `X-Tack-Client: web`；服务端拒绝缺失该头的 Cookie 认证请求。

### 12.5 Sudo 模式

以下操作要求 5 分钟内重新输入过密码：

- 应用更新 / 回滚
- 新建或撤销 API token
- 修改密码
- 删除项目
- 注册或删除 Passkey
- 新建、修改或删除 Webhook

### 12.6 传输与头部

- 生产必须 HTTPS，Caddy 自动签发。
- `Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'`
- `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`frame-ancestors 'none'`
- 不记录密码、token、Cookie 到日志。

### 12.7 威胁模型

- 公网暴露，单用户，攻击者主要尝试撞库和 API 扫描。
- `updater` 持有 docker.sock（等价 root），是本系统最高危组件；它不暴露端口、只接受固定命令、tag 必须匹配 `^v\d+\.\d+\.\d+$` 且在白名单仓库内。
- SQLite 文件权限 0600，数据目录 0700。

## 13. 前端设计

### 13.1 页面

| 路由 | 内容 |
|---|---|
| `/` | 看板：左侧项目/标签栏，顶部搜索，四列 |
| `/capture` | 手机全屏收集页（PWA `start_url`） |
| `/login` | 登录 |
| `/settings` | 常规：主题、语言、更新频率 |
| `/settings/account` | 密码、会话、Passkey |
| `/settings/tokens` | API token 管理 |
| `/settings/connections` | Agent 连接、API Key 管理与接入说明；旧 tokens 路由兼容 |
| `/settings/update` | 版本、更新日志、检查/更新/回滚 |

### 13.2 看板交互

```
┌──────────┬──────────────────────────────────────────────┐
│ 图钉      │  搜索…                          ⌘K    ⚙︎ ●   │
│          ├──────────────────────────────────────────────┤
│ 收件箱 7  │  Inbox 3     To do 5      Doing 2    Done 12 │
│ 全部      │  ┌────────┐  ┌────────┐  ┌────────┐          │
│ ─────     │  │ 想法：… │  │ 接入    │  │ 写迁移  │          │
│ @codeg    │  │ #随口   │  │ Codeg   │  │ #后端   │          │
│ @写书     │  └────────┘  └────────┘  └────────┘          │
│ ─────     │                                              │
│ #想法 12  │                                              │
└──────────┴──────────────────────────────────────────────┘
```

- 列：Inbox / To do / Doing / Done（review 有卡时出现）。
- 卡片：标题（两行截断）、项目色点、标签、优先级点、截止日。
- 拖拽用 dnd-kit：`⌘`/`Ctrl` + 方向键也能移动，保证键盘可达。
- 新建：列底「+」或 `n`；详情用右侧 Sheet（桌面）/ 底部 Sheet（手机）。
- 搜索：`⌘K` 命令面板，支持 `status:`、`tag:`、`project:` 前缀。
- 乐观更新：拖动先动 UI，失败回滚并 toast。

### 13.3 手机

- 底部导航：看板 / 收件箱 / **记一笔** / 搜索 / 设置，中间是主按钮。
- `/capture` 全屏输入，自动聚焦，`⌘↵` 或「存」提交，成功后清空并留在页面。
- PWA manifest：`display: standalone`、`theme_color` 跟随主题、图标 192/512。
- iOS 快捷指令：`POST /api/v1/capture`，token 认证；可加入分享菜单。

### 13.4 视觉

关键词：**编辑部的纸**。暖白、细线、衬线标题、一个赭红强调色，不用渐变和发光。

```css
:root {
  --paper: #FAF7F2;
  --surface: #FFFFFF;
  --ink: #201B16;
  --muted: #8A8178;
  --line: #E7DFD3;
  --accent: #B2532C;
  --accent-soft: #F3E4DA;
  --sage: #6F7D5B;
}

[data-theme="dark"] {
  --paper: #15120F;
  --surface: #1D1915;
  --ink: #F2EBE0;
  --muted: #9C9186;
  --line: #2E2823;
  --accent: #E08A63;
  --accent-soft: #33231B;
  --sage: #93A37C;
}
```

- 字体：标题 `Instrument Serif` + `Songti SC`；正文 `Inter` + `PingFang SC`；等宽 `JetBrains Mono`。
- 圆角 10px；卡片 1px 描边，无阴影；拖拽时才出现轻阴影。
- 动效 150–200ms `ease-out`；不加装饰性动画。
- 空状态用一句短文案 + 一个动作，不放插画。

### 13.5 性能预算

- 首屏 JS < 200KB gzip。
- 路由级 code split；看板以外的页面懒加载。
- 不引入图表库、不引入 moment/date-fns 全量包（用 `Intl` + 少量手写）。

### 13.6 现代 Web 能力基线

**Baseline 目标：Baseline 2024**（Chrome 115+ / Safari 18+ / Firefox 128+）。写进 `AGENTS.md`：

```
This project's Baseline target is Baseline 2024.
```

以此为界，可以放心直接用：容器查询、`:has()`、`text-wrap: balance`、`oklch()`、`<dialog>`、Popover API、View Transitions（同文档和跨文档）、`field-sizing`、`:user-invalid`、`color-mix()`。仍需 fallback 或不用：Anchor Positioning（Safari 支持晚）、`scroll-driven animations`（Safari 尚缺）。

不引入 Modern Web Guidance：它解决的是「模型不知道有原生能力」，而 shadcn/ui + Radix 已经把那批交互封装好了，两者边界重叠，多装一个只会让 agent 在「用组件还是用原生」之间摇摆。需要原生能力时按上面的清单直接选，拿不准就查 MDN 的 Baseline 表。

## 14. 更新机制

### 14.1 组件

- `app`：检查版本、展示 UI、把更新请求转给 updater。
- `updater`：独立镜像，挂 `docker.sock` 和 compose 目录，执行拉取/重建/回滚。
- `Caddy`：TLS 和反代。

`updater` 不参与自更新；升级它需要手动改 compose 里的 `TACK_UPDATER_TAG` 后 `up -d updater`。这是刻意留的“手动闸门”。

updater 只监听 `control` 网络的 `:8081`，接口四个，全部要求 `X-Updater-Token`：

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/health` | 自身存活 |
| `GET` | `/state` | 当前 tag、上一 tag、上次结果、备份路径 |
| `POST` | `/apply` | `{version}`，立刻返回 202，后台执行 |
| `POST` | `/rollback` | 恢复上一备份 + 上一 tag，返回 202 |

app 侧只暴露给已登录且处于 sudo 状态的用户，不把 updater token 传给浏览器。

### 14.2 更新流程

```
用户点「立即更新」
  → 输入密码（sudo）
  → app POST updater /apply {version}
  → updater 校验 tag、备份数据库
  → 改写 TACK_IMAGE_TAG 为 new
  → docker compose pull app
  → docker compose up -d --wait --wait-timeout 90 app
  → 健康检查通过：state.json 写 success
  → 失败：用旧 tag 重新 up -d --wait；写 rolled_back
```

- `--wait` 依赖 Dockerfile 的 `HEALTHCHECK`（`/healthz`）。
- 更新前备份：`VACUUM INTO '/data/backups/pre-update-<ts>.db'`，保留最近 5 份。
- **回滚同时恢复数据库**：新版本可能已跑过迁移，旧二进制未必认识新 schema；`/rollback` 先恢复更新前那份备份，再用旧 tag 重建容器。迁移本身仍遵守“只增不改 + 向前兼容一个版本”，备份是最后一道保险。
- `state.json` 放在 `/data/updater/`，app 可以直接读，用于重启后展示“上次更新成功/已回滚”。
- 前端在更新期间每 2 秒轮询 `/api/v1/version`，最多 120 秒；成功后 reload，超时提示查看日志。
- 回滚按钮只在 `state.json` 里有 `previous_tag` 时出现。
- updater 只接受形如 `vX.Y.Z` 且能在 `TACK_GITHUB_REPO` 的 release 列表里查到的 tag；不接受 `latest`、分支名或任意镜像。

### 14.3 版本检查

- app 里一个后台 goroutine，每 5 分钟判断是否到点。
- 频率可选：关闭 / 6 小时 / 12 小时 / 24 小时（默认）/ 48 小时。
- 调 `GET https://api.github.com/repos/Today-ddr/tackflow/releases/latest`，带 `If-None-Match`；结果写 `setting`。
- 同时缓存 release 的 `body`（Markdown），更新弹窗直接展示新版本改了什么；当前版本的历史看内嵌 `CHANGELOG.md`。
- 私有仓库时读 `TACK_GITHUB_TOKEN`（fine-grained，只需 `Contents: read`）。
- 有新版本：侧栏「设置」图标右上角红点；设置页显示版本对比和更新日志。
- 检查失败静默重试（退避 1h/6h/24h），不打扰用户；设置页显示上次检查错误。

### 14.4 版本比较

- 严格 SemVer，只比较 `major.minor.patch`；预发布版本默认不提示。
- 当前版本来自编译注入，不读文件。

## 15. 版本、变更日志与发布

### 15.1 单一版本源

- 仓库根 `VERSION` 文件，如 `0.3.0`。
- 编译注入：

```
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

### 15.2 发布脚本

`scripts/release.sh patch|minor|major`：

1. 读 `VERSION`，按 SemVer 递增；
2. 用 `git-cliff` 生成 `CHANGELOG.md` 新段落；
3. 跑 `make check`；
4. 本地提交 `chore(release): vX.Y.Z`；
5. 打本地 tag `vX.Y.Z`；
6. **打印**推送命令，不执行。

AI 可以在用户说“准备发版”后执行 1–5；第 6 步必须等用户明确说“推送”。

### 15.3 CI

`.github/workflows/ci.yml`（push / PR）：

- `gofmt -l`、`go vet`、`golangci-lint`、`go test ./...`
- `pnpm install --frozen-lockfile`、`pnpm lint`、`pnpm typecheck`、`pnpm test`、`pnpm build`

`.github/workflows/release.yml`（tag `v*`）：

1. 校验 `VERSION` 与 tag 一致；
2. `docker/setup-buildx-action`；
3. 构建 `linux/amd64,linux/arm64`；
4. 推 GHCR：`vX.Y.Z`、`sha-<short>`、`latest`；
5. 用 `git-cliff --latest` 生成 release notes，创建 GitHub Release；
6. OCI 标签写入 source / revision / version。

仓库默认 **public**，GHCR 包也 public，服务器拉取不需要凭证。要私有的话：仓库加 `TACK_GITHUB_TOKEN` 用于版本检查，服务器 `docker login ghcr.io` 用于拉取。

### 15.4 更新日志

- 来源：Conventional Commits → `git-cliff`（配置在 `cliff.toml`）。
- 分组：新增 / 修复 / 优化 / 文档 / 其他。
- `CHANGELOG.md` 打进镜像，`/api/v1/changelog` 直接返回，离线可看。

## 16. 部署

### 16.1 compose 结构

服务器上的目录就是仓库 `deploy/` 的内容，放到 `/opt/tack`：

```
/opt/tack/
├── docker-compose.yml
├── .env
├── Caddyfile
└── data/
```

`.env` 里固定 `COMPOSE_PROJECT_NAME=tack`，updater 执行 `docker compose` 时无论 cwd 在哪都落到同一个项目上。updater 容器内自带 docker CLI + compose 插件。

```yaml
services:
  app:
    image: ghcr.io/today-ddr/tackflow:${TACK_IMAGE_TAG:-latest}
    env_file: .env
    volumes:
      - ./data:/data
    networks: [web, control]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/tack", "healthcheck"]
      interval: 15s
      timeout: 3s
      retries: 3

  updater:
    image: ghcr.io/today-ddr/tackflow-updater:${TACK_UPDATER_TAG:-v0.1.0}
    env_file: .env
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./:/compose
      - ./data:/data
    networks: [control]
    restart: unless-stopped

  caddy:
    image: caddy:2
    ports: ["80:80", "443:443"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
    networks: [web]
    restart: unless-stopped
```

### 16.2 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `TACK_ADMIN_PASSWORD` | 首次启动 | 初始化密码，之后忽略 |
| `TACK_ADMIN_USERNAME` | 否 | 默认 `admin` |
| `TACK_PUBLIC_URL` | 是 | 如 `https://tack.example.com` |
| `TACK_SESSION_SECRET` | 是 | 32 字节 base64，Cookie 签名 |
| `TACK_DATA_DIR` | 否 | 默认 `/data` |
| `TACK_IMAGE_TAG` | 是 | 当前运行版本，updater 会改写 |
| `TACK_UPDATER_TAG` | 是 | updater 自己的版本，手动升级 |
| `TACK_UPDATER_URL` | 是 | app 访问 updater 的地址，Compose 默认 `http://updater:8081` |
| `TACK_UPDATER_TOKEN` | 是 | app ↔ updater 共享密钥 |
| `TACK_COMPOSE_FILE` | 是 | updater 用，默认 `/compose/docker-compose.yml` |
| `TACK_ENV_FILE` | 是 | updater 用，默认 `/compose/.env` |
| `TACK_SERVICE` | 是 | updater 用，默认 `app` |
| `TACK_GITHUB_REPO` | 是 | `Today-ddr/tackflow` |
| `TACK_GITHUB_TOKEN` | 否 | 私有仓库读 release 用 |
| `TACK_TZ` | 否 | 默认 `Asia/Shanghai`，只影响展示 |

updater 改写 `.env` 时只替换 `TACK_IMAGE_TAG=` 这一行，写临时文件后原子 rename，其余行原样保留；同时把旧值写进 `data/updater/state.json` 供回滚。

### 16.3 备份与恢复

- 每日 03:00 `VACUUM INTO` 到 `/data/backups/daily-YYYYMMDD.db`，保留 14 天。
- 更新前额外备份，保留 5 份。
- 恢复：停 app → 替换 `tack.db` → 起 app。
- 文档写在 `docs/runbook.md`。

### 16.4 健康检查

- `/healthz`：进程活着，不查 DB，供 Docker 用。
- `/readyz`：`SELECT 1` + 迁移版本一致，供 Caddy/人工用。

### 16.5 运维命令

二进制内置几个不依赖 HTTP 的子命令，docker exec 直接用：

- `tack reset-password`：交互式重置密码（忘记密码时的唯一入口），会踢掉所有 session。
- `tack backup`：立即做一次 `VACUUM INTO`。
- `tack migrate`：只跑迁移，不启动服务。
- `tack healthcheck`：给 Docker `HEALTHCHECK` 用。

## 17. 代码组织

```
tack/
├── AGENTS.md                 # AI 入口：命令、红线、代码地图（≤120 行）
├── README.md
├── Design.md                  # 本文件
├── CHANGELOG.md
├── VERSION
├── Makefile
├── go.mod
├── cmd/
│   ├── tack/main.go          # app 入口
│   └── updater/main.go       # sidecar 入口
├── internal/
│   ├── api/                  # 路由、中间件、请求/响应 DTO
│   ├── auth/                 # 密码、session、token、sudo
│   ├── capture/              # 内联语法解析
│   ├── store/                # SQLite、迁移、查询
│   ├── tasks/                # 领域逻辑（状态流转、排序）
│   ├── update/               # 版本检查、updater 客户端
│   └── platform/             # 日志、request id、时钟
├── migrations/
│   └── 0001_init.sql
├── web/
│   ├── index.html
│   ├── src/
│   │   ├── app/              # 路由、布局
│   │   ├── components/       # 业务组件
│   │   ├── components/ui/    # shadcn 复制件
│   │   ├── lib/              # api client、query、工具
│   │   └── styles/
│   └── dist/                 # 构建产物（gitignore，编译时生成）
├── deploy/
│   ├── docker-compose.yml
│   ├── Caddyfile
│   └── .env.example
├── docs/
│   ├── api.md
│   ├── runbook.md
│   └── adr/
├── scripts/
│   ├── release.sh
│   └── dev.sh
└── .github/workflows/
```

原则：

- `internal` 按领域分，不按 `handlers/services/repositories` 三层切。
- `api` 只做解析和响应，业务规则在 `tasks`。
- `store` 不依赖 `api`；`tasks` 不依赖 HTTP。
- 前端 `components/ui` 只放 shadcn 原件，业务样式写在业务组件里。

## 18. AI 协作规范

这一节是给后续所有 AI 会话的约束，落地时同步进 `AGENTS.md`。

### 18.1 每次开工

1. 读 `AGENTS.md`；
2. 读本文件里与任务相关的章节；
3. `git status` 和 `git log --oneline -10`，不要在用户未提交的改动上动手；
4. 先看现有实现，再改；不重复造已有函数。

### 18.2 质量门

`make check` 必须全绿才能提交：

- `gofmt -l .` 无输出
- `go vet ./...`
- `golangci-lint run`
- `go test ./...`
- `pnpm -C web lint`
- `pnpm -C web typecheck`
- `pnpm -C web test`
- `pnpm -C web build`

新功能必须有测试；修 bug 先写能复现的测试。

### 18.3 提交

- Conventional Commits：`type(scope): 中文描述`
- type：`feat` `fix` `refactor` `perf` `test` `docs` `chore` `build` `ci`
- scope：`api` `web` `db` `auth` `updater` `deploy` `ci` `docs`
- 一个提交只做一件事；正文 2–5 条，写清“为什么”和“影响面”。
- 不改写已推送的历史；用户没让 amend 就不 amend。

### 18.4 数据库

- 迁移只增不改；已合并的 `000N_*.sql` 永远不动。
- 新迁移必须能在空库和上一版库上都跑通。
- 所有列表查询必须有索引；新增查询先看 `EXPLAIN QUERY PLAN`。
- 时间一律 UTC，展示时再转本地。

### 18.5 API

- `/api/v1` 只做向后兼容的增量；破坏性变更开 `/api/v2`。
- 错误码一旦发布就稳定，只能新增。
- 新增字段必须可选；删除字段至少保留两个 minor。
- 每个 handler 记录 `request_id`、路径、状态码、耗时；不记录 body。

### 18.6 前端

- TypeScript strict，禁止 `any`（确实需要时用 `unknown` + 收窄）。
- 服务端状态只走 TanStack Query，组件里不直接 `fetch`。
- 颜色只用 CSS 变量/token，禁止写死 hex。
- 新组件先查 shadcn 和现有 `components/`，没有再新建。
- 所有交互必须有键盘路径和 loading/empty/error 三态。
- 写不熟悉的浏览器能力前，先查 MDN 的 Baseline 表，确认在 §13.6 的清单内；不在清单内的先问，不自作主张。
- 不为「用原生 API」而绕过 shadcn/ui 已封装的 dialog / popover / menu。

### 18.7 依赖

- 新增运行时依赖必须在提交正文说明：为什么标准库/现有依赖不够。
- 禁止 cgo；禁止需要额外容器或外部服务的依赖。
- 前端单个依赖超过 30KB gzip 需要说明。

### 18.8 发布红线

- AI 可以改 `VERSION`、`CHANGELOG.md`、打本地 tag。
- **AI 不得自动 `git push`，不得推远端 tag。**
- 用户明确说“推送 / 发布 / 推上去”后才执行。
- 推送前必须跑 `make check` 和 `make build`，并报告版本号和变更摘要。

### 18.9 文档

- 契约变化同步 `docs/api.md` 和本文件对应章节。
- 重要取舍写 `docs/adr/NNNN-<slug>.md`，一页以内：背景、选项、决定、后果。
- `README.md` 只保留：是什么、怎么跑、怎么部署、去哪看文档。

### 18.10 可用的 agent skill

本机已装、与本项目相关的能力（`npx skills check` / `npx skills update` 管理，下表是 2026-11-16 的状态）：

| skill | 来源 | 什么时候用 |
|---|---|---|
| `design-taste-frontend` | Leonxlnx/taste-skill | 定视觉方向、排版、配色；新建或评审页面时 |
| `animate` | emilkowalski/skills | 加动效 |
| `animation-performance` | emilkowalski/skills | 查掉帧、选动画属性 |
| `improve-animations` | emilkowalski/skills | 只读审计全站动效，产出改进计划 |
| `find-animation-opportunities` | emilkowalski/skills | 只读寻找「这里是否值得加动效」 |
| `review-animations` | emilkowalski/skills | 动效代码从严评审 |
| `mobile-native` | emilkowalski/skills | PWA 的 100vh、点击高亮、输入缩放、安全区这些坑 |
| `break-ui` | emilkowalski/skills | 用超长标题、空数据、emoji、极端数字压测界面 |
| `better-icons` | better-auth/better-icons | 找图标（Iconify） |
| `commit-tidy` | 本地 | 提交变散时整理历史 |

使用边界：这些 skill 只给方法，不覆盖本文件的决定（组件库、token、Baseline、API 契约）。冲突时以本文件为准。

2026-11-16 已执行 `npx --yes skills@latest update -g -y`。其中 `design-taste-frontend`、`animate`、`animation-performance`、`improve-animations`、`mobile-native`、`review-animations`、`break-ui`、`find-animation-opportunities` 共 8 个成功更新；`better-icons` 与 `commit-tidy` 本次未更新。11 个 superpowers 开发流程 skill 已从 `~/.codeg/skills` 软链到 `~/.codex/skills`，供后续计划、TDD、验证和代码评审使用。

## 19. 测试策略

| 层 | 工具 | 覆盖 |
|---|---|---|
| 单元 | `go test` | capture 解析、position 计算、密码哈希、版本比较 |
| 集成 | `go test` + 临时 SQLite | 迁移、任务 CRUD、状态流转、幂等、token scope |
| API | `httptest` | 认证、错误码、分页、CSRF |
| 前端 | Vitest + Testing Library | 看板排序、乐观更新、快捷键、表单校验 |
| 冒烟 | 手动 + `scripts/dev.sh` | 登录、建卡、拖拽、手机 capture、更新流程（staging） |

v1 不追求 E2E 覆盖率；更新流程靠 staging 手测 + updater 单元测试（命令参数、tag 校验、回滚决策）。

本地开发：`make dev` 同时起 Vite（5173）和 Go（8080），Vite 把 `/api` 代理到 8080；数据库默认 `./data/dev.db`，不碰生产数据。

## 20. 里程碑

| 阶段 | 内容 | 验收 |
|---|---|---|
| M0 骨架 | 仓库、Makefile、compose、健康检查、CI、登录 | `docker compose up` 能登录，CI 绿 |
| M1 核心 | 任务 CRUD、看板拖拽、Inbox、项目/标签、capture 解析 | 手机上能记，桌面上能拖 |
| M2 API | token、幂等、分页、curl 文档 | 脚本可建卡/取卡 |
| M3 更新 | 版本注入、changelog、版本检查、红点、updater、回滚 | 网页点一下完成升级 |
| M4 打磨 | 搜索、⌘K、暗色、PWA、导出、备份 | 日常自用一周无阻断 |
| M5 扩展 | Passkey、Codeg bridge、webhook、SSE | 按需 |

当前实现状态（2026-11-16）：M0–M4 已落地；工作分支在 `v0.2.0` 基础上追加 M5，尚未发布新版本。更新链路继续使用 app 检查、侧栏红点、可调频率、updater、备份和失败回滚。

M5 包含 Passkey、独立 Codeg bridge、持久化 Webhook 与 Session SSE；同时补齐回收站、项目/标签管理、全部游标页加载、搜索前缀和 Token 资源范围。详细接口见 `docs/api.md`，桥接配置见 `docs/codeg-bridge.md`。真实设备 Passkey、实际 Codeg 执行和公网接收端需在对应部署环境验收。

Token 的项目和标签数组为空表示该维度不限制，维度内 OR、维度间 AND；旧 Token 不变。受限 Token 不能写全局分类或自动创建标签，列表/导出计数不泄露范围外任务。

Agent 连接（2026-11-17）：继续以 REST v1 为权威接口，将 Token 管理收敛到「Agent 连接」。每个 Agent 建独立 API Key，提供只读、收集、任务执行和自定义用途预设；网页默认有效期 30 天，可选 7/90/365 天或不过期。新增可选 `expires_at`（未来 365 天内的 RFC3339，统一 UTC 秒精度），迁移 0010 保留旧 Key 不过期。每次认证和取得写锁后都复核到期；过期未撤销项仍展示在管理列表。

`GET /api/v1/agent/connection` 是任何有效 Token 都可调用的只读认证检查，网页 Session 也能读取接入地址，但响应明确区分身份。返回受信的 `TACK_PUBLIC_URL` API 地址及当前 Token 自身非敏感元数据，不返回任务、秘密或其他凭证。网页检查固定同源、仅 Bearer、不带 Cookie、不跟随重定向。复制的 Agent 说明按权限/资源生成，不嵌入明文 Key；说明与凭证分开交付。最近请求仅表示凭证使用记录，不推断 Agent 在线。

当前不提供 MCP endpoint，也不将 REST 地址伪装成 MCP。需要聊天客户端自动发现工具时再添加复用 API 的适配器；远程授权方案届时按客户端和 MCP 规范设计。取舍及官方依据见 `docs/adr/0001-agent-connections.md`，使用步骤见 `docs/agent-connections.md`。

Agent 只原子领取 `todo + agent_ready`，使用绑定主体的 30 分钟租约；GET 只预览，POST 领取。心跳续租，结果默认 `review`；过期或人工改动后旧 worker 不能回写。

SQLite 事务内写入资源事件；SSE 用持久化序号恢复，Webhook 用持久化投递队列重试并签名。Webhook 默认禁止内网目标和跳转，秘密仅保存在本地数据库，不出现在导出和日志。数据库和备份文件权限为 0600。

## 21. 风险

| 风险 | 影响 | 对策 |
|---|---|---|
| docker.sock 等价 root | 最高 | updater 不暴露端口、固定命令、tag 白名单、独立 token |
| 自更新把服务弄挂 | 高 | 备份 + `--wait` 健康检查 + 自动回滚 + 保留上一 tag |
| GitHub API 限流 | 中 | ETag、最小 6h 间隔、可选 token、失败退避 |
| SQLite 写并发 | 低 | 单用户；WAL + busy_timeout；写操作串行 |
| iOS PWA 限制 | 中 | 快捷指令兜底；不做后台同步/推送 |
| 单作者项目停更 | 中 | 代码量与依赖克制；数据是纯 SQLite，随时可迁 |
| 名字冲突 | 低 | 早期可改名，成本仅目录和 module path |

## 22. 已定决策

1. 自建，不套 Vikunja。
2. 单用户、单板；项目是彩色分类。
3. Go + SQLite + React/Vite + shadcn/ui。
4. 密码 + Session + API Token + Passkey；不接微信 / Apple / Bitwarden OAuth。
5. updater sidecar 负责更新，app 不碰 docker.sock。
6. 更新默认每天检查一次，频率可调；侧栏红点提示。
7. AI 负责改版本和 changelog，推送必须人工点头。
8. 仓库默认 public，GHCR public；私有部署方案写在 runbook。
9. 项目名使用 `Tack`，公开仓库为 `Today-ddr/tackflow`；Go 模块与 GHCR 镜像路径使用小写 `today-ddr/tackflow`。
10. M1 先完成 Inbox + 单板；多项目筛选延后。

## 23. 待定

- 域名（部署前定）。
- M5 发布版本号与目标部署环境（当前仅本地提交，不自动发布）。
