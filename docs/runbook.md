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
TACK_GHCR_OWNER=<github-owner>
TACK_IMAGE_TAG=v0.2.0
TACK_UPDATER_TAG=v0.2.0
TACK_SITE_ADDRESS=tack.example.com
TACK_PUBLIC_URL=https://tack.example.com
TACK_ADMIN_USERNAME=admin
TACK_ADMIN_PASSWORD=<首次启动密码，至少 12 位>
TACK_SESSION_SECRET=<base64 32 字节>
TACK_GITHUB_REPO=<github-owner>/tack
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

如果仓库私有：

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

同时给 `TACK_GITHUB_TOKEN` 配置 fine-grained token，至少允许 `Contents: read`，用于读取 Release。不要把 token 写进 compose 文件或提交到仓库。

## 网页更新

1. 在 GitHub 推送 tag 后，Release workflow 构建并推送：
   - `ghcr.io/<owner>/tack:<tag>`
   - `ghcr.io/<owner>/tack:sha-<short>`
   - `ghcr.io/<owner>/tack-updater:<tag>`
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

## 手机与快捷指令

- PWA：打开 `https://tack.example.com/capture`，添加到主屏幕；manifest 的 `start_url` 就是 capture。
- iOS 快捷指令：创建「获取 URL 内容」请求，方法 `POST`，URL 为 `https://tack.example.com/api/v1/capture`，Authorization 为 `Bearer tk_...`，请求体 `{"text":"..."}`。
- Token 在「设置 → API 令牌」创建，明文只显示一次。

## 安全边界

- 公网只暴露 Caddy 的 80/443。
- updater 持有 Docker socket，等价 root；不要把它加入 `web` 网络或发布端口。
- 所有更新 tag 必须是 `vX.Y.Z` 且能在 GitHub Release 中查到。
- 不记录密码、Cookie、API Token；`.env` 和 `data/` 权限保持 `0600`/`0700`。
