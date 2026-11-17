# Tack Runbook

## 部署拓扑

服务器目录只放 `deploy/` 的内容：

```text
/opt/tack/
├── docker-compose.yml
├── Caddyfile
├── .env
└── data/
    ├── tack.db
    ├── backups/
    └── updater/state.json
```

- `app`：Go 单二进制，内嵌前端和 SQLite，监听 `:8080`。
- `caddy`：唯一公网入口，负责 80/443 和 TLS。
- `updater`：只在内部 `control` 网络运行，挂载 Docker socket，负责拉取镜像、重建 app、备份和回滚。
- `app` 不接触 Docker socket；updater 不发布端口。

## 首次部署

```bash
cd /opt/tack
cp .env.example .env
chmod 600 .env
openssl rand -base64 32   # TACK_SESSION_SECRET
openssl rand -hex 32      # TACK_UPDATER_TOKEN
```

生产环境至少修改：

```dotenv
COMPOSE_PROJECT_NAME=tack
TACK_GHCR_OWNER=today-ddr
TACK_IMAGE_TAG=v0.2.0
TACK_UPDATER_TAG=v0.2.0
TACK_SITE_ADDRESS=tack.example.com
TACK_PUBLIC_URL=https://tack.example.com
TACK_ADMIN_USERNAME=admin
TACK_ADMIN_PASSWORD=<首次启动密码，至少 12 位>
TACK_SESSION_SECRET=<base64 32 字节>
TACK_GITHUB_REPO=Today-ddr/tackflow
TACK_GITHUB_TOKEN=
TACK_UPDATER_URL=http://updater:8081
TACK_UPDATER_TOKEN=<与上面同一密钥>
TACK_COMPOSE_FILE=/compose/docker-compose.yml
TACK_ENV_FILE=/compose/.env
TACK_SERVICE=app
```

`TACK_PUBLIC_URL` 的 scheme、host、port 必须与浏览器实际访问地址一致，否则登录 Cookie 和 CSRF Origin 校验会失败。首次启动后 `TACK_ADMIN_PASSWORD` 会被忽略。

```bash
docker compose --env-file .env -f docker-compose.yml pull
docker compose --env-file .env -f docker-compose.yml up -d
docker compose --env-file .env -f docker-compose.yml ps
curl -fsS https://tack.example.com/healthz
```

首次启动如果 `data/` 不存在，确认目录对 `TACK_UID:TACK_GID` 可写：

```bash
mkdir -p data
sudo chown -R "$(id -u):$(id -g)" data
chmod 700 data
```

## GHCR 可见性

默认仓库和镜像 public，服务器无需登录即可 `pull`。

源码仓库为 `Today-ddr/tackflow`；GHCR 镜像路径使用小写 `today-ddr/tackflow` 和 `today-ddr/tackflow-updater`，`TACK_GHCR_OWNER` 也需保持小写。

如果仓库私有：

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

同时给 `TACK_GITHUB_TOKEN` 配置 fine-grained token，至少允许 `Contents: read`，用于读取 Release。不要把 token 写进 compose 文件或提交到仓库。

## 网页更新

1. 在 GitHub 推送 tag 后，Release workflow 构建并推送：
   - `ghcr.io/today-ddr/tackflow:<tag>`
   - `ghcr.io/today-ddr/tackflow:sha-<short>`
   - `ghcr.io/today-ddr/tackflow-updater:<tag>`
2. app 定时请求 GitHub Releases，发现新版本后侧栏设置图标出现红点。
3. 进入「设置 → 更新」，点击「立即更新」，输入当前密码确认 sudo。
4. app 调 updater；updater 的流程是：

```text
验证 tag + 检查 Release 存在
  → app 容器内 tack backup
  → 改写 .env 的 TACK_IMAGE_TAG
  → docker compose pull app
  → docker compose up -d --wait app
  → 健康检查通过则记录 state.json
  → 失败则停 app、恢复备份、恢复旧 tag、重建 app
```

更新前数据库备份位于 `data/backups/tack-*.db`，每次更新还会保留 `state.json` 中的备份路径。回滚按钮只在存在 `previous_tag` 时出现。

`updater` 不参与自更新：升级 updater 要手动改 `.env` 的 `TACK_UPDATER_TAG`，然后：

```bash
docker compose --env-file .env -f docker-compose.yml pull updater
docker compose --env-file .env -f docker-compose.yml up -d updater
```

这是刻意保留的人工闸门。不要给 updater 发布公网端口。

## 数据备份与恢复

每日维护任务会写：

```text
data/backups/daily-YYYYMMDD.db
```

保留 14 天。立即备份：

```bash
docker compose --env-file .env -f docker-compose.yml exec -T app /tack backup
```

恢复步骤：

```bash
docker compose --env-file .env -f docker-compose.yml stop app
cp data/backups/<backup>.db data/tack.db
rm -f data/tack.db-wal data/tack.db-shm
docker compose --env-file .env -f docker-compose.yml up -d app
curl -fsS https://tack.example.com/readyz
```

恢复前先把当前 `tack.db` 复制到安全位置。不要把 `data/` 或 `.env` 提交到 Git。

## 数据库迁移

迁移只增不改，SQL 内嵌在二进制里。正常启动自动迁移；手工执行：

```bash
docker compose --env-file .env -f docker-compose.yml exec -T app /tack migrate
```

升级前先备份。旧版本数据库应先让新版本跑过迁移，再接受写入；如果迁移失败，恢复上一份备份和上一镜像。

## 发布流程

发布由本地准备、人工推送、CI 构建三部分组成：

```bash
./scripts/release.sh minor
```

脚本会：

1. 从 `VERSION` 递增版本；
2. 如果本机有 `git-cliff`，生成 `CHANGELOG.md` 新段落；
3. 跑 `make check`；
4. 本地提交 `chore(release): vX.Y.Z`；
5. 打本地 tag；
6. 打印 push 命令，**不执行 push**。

推送必须由用户明确确认：

```bash
git push origin HEAD
git push origin vX.Y.Z
```

tag 推送后 `.github/workflows/release.yml` 会校验 `VERSION` 与 tag 一致，跑质量门，构建多架构 app/updater 镜像，创建 GitHub Release。

## 健康检查与故障排查

```bash
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs --tail=200 app updater caddy
curl -fsS https://tack.example.com/healthz
curl -fsS https://tack.example.com/readyz
curl -fsS https://tack.example.com/api/v1/version
```

常见问题：

- **登录后没 Cookie**：检查 `TACK_PUBLIC_URL` 与地址是否完全一致；HTTPS 必须使用 `__Host-tack_session`。
- **更新按钮报 `update_unavailable`**：检查 `TACK_UPDATER_URL`、`TACK_UPDATER_TOKEN` 和 updater 日志。
- **更新一直 running**：看 updater `/state` 和 `docker compose` 日志；确认镜像 tag 已推送到 GHCR。
- **更新后 SQLite 不可写**：确认 `data/` 和 `tack.db` 的 UID/GID 与 `TACK_UID:TACK_GID` 一致。
- **版本检查失败**：确认 `TACK_GITHUB_REPO`、仓库可见性和 `TACK_GITHUB_TOKEN`；私有仓库需要 token。
- **Caddy 拿不到证书**：确认 DNS、80/443 入站和 `TACK_SITE_ADDRESS`。

## 网页诊断日志

「设置 → 日志」读取 Tack 服务端的请求、错误和维护事件，可按级别、请求 ID 筛选，按路由/错误码搜索，并复制或下载当前结果的 JSONL。页面报错会附带请求 ID，可用它定位同一请求的路由、HTTP 状态、错误码和毫秒耗时；复制的 JSONL 可直接交给 AI 分析。

日志持久保存在 `TACK_DATA_DIR/logs/tack-0.jsonl`，旧文件轮转为 `tack-1.jsonl`、`tack-2.jsonl`，每个文件最多 1 MiB。目录权限 0700、文件 0600；服务重启后恢复最多 500 条最近记录。`GET /api/v1/logs?level=ERROR&limit=200&request_id=req_...` 只接受网页 Session，响应禁止缓存；API Key 不能读取。日志只收白名单字段，不记录请求正文、查询参数、原始 URL、密码、Cookie、API Key 或任务内容；外部错误中的凭证和 URL 查询参数会脱敏。

无网页连接时，可在服务器读取完整轮转窗口：

```bash
docker compose --env-file .env -f docker-compose.yml exec -T app /tack healthcheck
jq -s 'sort_by(.time)[] | select(.level == "ERROR" or .level == "WARN")' data/logs/tack-*.jsonl
```

这不是审计日志或所有组件的日志聚合；浏览器 JS 异常查看浏览器控制台，升级重建/回滚查看 updater，TLS 与入口网络查看 Caddy。日志达到容量会淘汰旧记录；遇到问题尽早复制相关时间窗口和 `/api/v1/version` 信息。

## 手机与快捷指令

- PWA：打开 `https://tack.example.com/capture`，添加到主屏幕；manifest 的 `start_url` 就是 capture。
- iOS 快捷指令：创建「获取 URL 内容」请求，方法 `POST`，URL 为 `https://tack.example.com/api/v1/capture`，Authorization 为 `Bearer tk_...`，请求体 `{"text":"..."}`。
- Token 在「设置 → Agent 连接」创建，明文只显示一次；快捷指令选择「快速收集」。

## M5 功能配置

- 「设置 → 项目与标签」管理分类与归档；「回收站」恢复删除未满 30 天的任务。删除标签只解绑任务。受限 Token 的项目/标签范围在创建时固定，需要调整时新建并撤销旧 Token。
- Passkey 在账户设置注册，需要最近验证过密码。RP ID 固定为 `TACK_PUBLIC_URL` 的主机名；更换域名后旧 Passkey 不能在新域名登录，请先保留密码访问并重新注册。生产必须 HTTPS，localhost 可开发验证。系统认证取消不会影响密码登录。
- SSE 随 app 启动，浏览器断线后按事件序号恢复；反向代理需允许长连接和即时 flush。Caddy `reverse_proxy` 可直接传递 `text/event-stream`。故障时先检查 `/api/v1/events` 的响应类型、心跳与 Session 是否有效。
- Webhook 在集成设置管理，新增后立即保存唯一展示的 secret。默认只允许 HTTPS 公网目标；创建时检查 DNS，投递时重新检查。仅受控本地测试设置 `TACK_WEBHOOK_ALLOW_PRIVATE=true`，默认 `false`。
- 投递由 app 内的工作循环处理，重启后继续数据库队列；最多 5 次，单次 5 秒超时。界面展示投递状态和错误；接收方按 delivery ID 去重、检查时间戳并验证 HMAC。禁用/删除会取消待投递，已在网络中的请求可能完成。
- 数据库包含 Webhook 签名密钥；`tack.db`、WAL/SHM、备份均须私密保存，备份不能公开下载。JSON/CSV 导出不包含密钥、Session、Passkey 或 Token。
- `make build` 额外生成 `bin/tack-bridge`。桥接作为独立进程显式启动，不随 app/compose 自动执行任务。配置、只读检查和恢复方法见 [Codeg bridge](codeg-bridge.md)。

升级前运行 `make check`，备份数据库，再按现有更新流程部署。新增迁移为 0003–0009，保留原任务、Session 和无资源限制的 Token。删除授权标签会撤销引用该标签的 Token；任务物理清理后编号不复用。幂等缓存由 Session secret 加密，恢复数据库时保留原密钥；`idempotency_uncertain` 表示应先查现状再决定重试，不能直接换 key 再发。当前工作分支尚未打新版本 tag；部署域名和 GitHub owner 沿用操作者的环境配置。

验证 Passkey 时应在真实 HTTPS 域名或 localhost 使用系统认证器完成注册、退出、重新登录、删除；自动化签名测试不能替代系统 Face ID/指纹体验。桥接首次使用先执行 `--check`，再在专用测试项目中放一张 `todo + agent_ready` 卡，检查 Codeg 任务、心跳和回写；不要用生产任务作冒烟测试。

## Agent 连接与 Key

「设置 → Agent 连接」管理 API Key、接入说明和只读检查，旧 `/settings/tokens` 路径仍有效。已登录网页可直接创建 Key，不再要求重复输入密码；创建仍受 Session、CSRF 和 Origin 校验保护，撤销仍要求 sudo。新网页创建默认 30 天，可选 7/90/365 天或不过期；旧 Key 不设置默认有效期。迁移 0010 仅追加 `api_token.expires_at`，启动新版本前按常规流程备份。

检查只向当前 Tack 发认证 GET；远程 Agent 需另在自身运行环境执行说明中的 curl，且不能把 localhost 当服务器公网地址。最近请求含检查请求，不是在线状态。更换 Key 先结束进行中的领取，创建/配置/验证新 Key，再撤销旧 Key；已有 claim 不能转移给新凭证。

Tack 当前没有 MCP endpoint；REST API 和 Codeg bridge 的使用不依赖 MCP。完整步骤见 [Agent 连接](agent-connections.md)，协议取舍见 [ADR 0001](adr/0001-agent-connections.md)。

## 安全边界

- 公网只暴露 Caddy 的 80/443。
- updater 持有 Docker socket，等价 root；不要把它加入 `web` 网络或发布端口。
- 所有更新 tag 必须是 `vX.Y.Z` 且能在 GitHub Release 中查到。
- 不记录密码、Cookie、API Token；`.env` 和 `data/` 权限保持 `0600`/`0700`。
