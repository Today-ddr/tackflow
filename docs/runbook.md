# Tack Runbook

## 服务器目录

部署目录只需要 `deploy/` 的内容：

```text
/opt/tack/
├── docker-compose.yml
├── .env
├── Caddyfile
└── data/
```

`deploy/.env` 不入库。生产环境将 `TACK_PUBLIC_URL` 改为 HTTPS 域名，`TACK_SITE_ADDRESS` 改为同一个域名，Caddy 自动签发 TLS。

## 首次启动

```bash
openssl rand -base64 32     # 填入 TACK_SESSION_SECRET
docker compose --env-file .env -f docker-compose.yml up -d
```

首次启动必须提供至少 12 位的 `TACK_ADMIN_PASSWORD`。数据库一旦创建管理员，后续启动会忽略该变量；不要在服务器上把密码写进命令历史。

## 本地 HTTP

本地 Compose 使用：

```dotenv
TACK_PUBLIC_URL=http://localhost
TACK_SITE_ADDRESS=:80
```

服务端会使用 `tack_session`，不会伪造 `__Host-` Cookie。生产 HTTPS 才使用 `__Host-tack_session`。

## 数据与备份

- 数据库：`/data/tack.db`，WAL 模式。
- 立即备份：`docker compose exec app /tack backup`
- 备份写到 `/data/backups/tack-YYYYMMDD-HHMMSS.db`。
- 恢复：停止 app → 替换 `data/tack.db` → 启动 app。恢复前先保留当前文件。
- 迁移命令：`docker compose exec app /tack migrate`

## 健康检查

- `/healthz`：进程和容器健康检查。
- `/readyz`：数据库可连接。
- Docker 健康命令：`/tack healthcheck`。

## 故障排查

1. 看 `docker compose ps` 和 `docker compose logs app caddy`。
2. 若登录 Cookie 不生效，确认 `TACK_PUBLIC_URL` 与浏览器实际地址的 scheme/host/port 一致。
3. 若容器无法写数据，确认 `deploy/data` 对 `TACK_UID:TACK_GID` 可写。
