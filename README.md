# Tack

Tack（图钉）是一个单用户、单看板的自建任务收集器。手机 3 秒记一笔，桌面拖卡整理，脚本和 agent 通过 API 读写；数据只在自己的 SQLite 文件里。

当前版本：**0.2.0（M1–M4 可用版）**。

## 能力

- Inbox / 待办 / 进行中 / 待验收 / 已完成五列看板，dnd-kit 拖拽，失败自动回滚。
- 快速收集语法：`#标签`、`@项目`、`!p0`–`!p3`、`^今天/明天/后天/日期`。
- 项目彩色分类、标签、任务详情、时间线、评论、软删除和回收站清理。
- 手机全屏 `/capture`、PWA manifest、底部导航和安全区适配。
- 单用户密码 + Session Cookie + sudo；脚本用可撤销、按 scope 限权的 API Token。
- API v1：任务 CRUD/move/comments/events、capture、项目、标签、设置、JSON/CSV 导出。
- GitHub Release 检查、可调频率、侧栏红点、网页内 updater、更新前备份和失败自动回滚。
- Go 单二进制 + SQLite + 内嵌前端；Docker Compose 只暴露 Caddy。

## 本地开发

```bash
pnpm -C web install --frozen-lockfile
make dev
```

`make dev` 会在 `deploy/.env` 不存在时生成一份本地环境，启动 Vite（<http://localhost:5173>）和 Go API（<http://localhost:8080>）。Vite 会把 `/api` 代理到 Go。

常用命令：

```bash
make check              # gofmt / vet / lint / Go test / 前端 lint+typecheck+test+build / 双二进制
make build              # 生成 bin/tack 和 bin/tack-updater
make docker-build       # 构建 app 镜像
make docker-build-updater
```

## Docker 部署

```bash
cp deploy/.env.example deploy/.env
# 至少修改 TACK_PUBLIC_URL、TACK_SESSION_SECRET、TACK_ADMIN_PASSWORD、
# TACK_UPDATER_TOKEN、TACK_GHCR_OWNER 和两个 image tag
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

浏览器打开 `TACK_PUBLIC_URL`。生产 HTTPS 使用 `__Host-tack_session`，本地 HTTP 自动使用 `tack_session`。完整部署、备份、更新和回滚见 [运维手册](docs/runbook.md)。

## API

```bash
# 登录后 Cookie 请求
curl -fsS -b /tmp/tack.cookies \
  -H 'X-Tack-Client: web' \
  -H 'Content-Type: application/json' \
  -d '{"text":"修登录跳转 #bug @codeg !p2 ^明天"}' \
  http://localhost:8080/api/v1/capture

# 手机快捷指令 / 脚本
curl -fsS \
  -H 'Authorization: Bearer tk_...' \
  -H 'Content-Type: application/json' \
  -d '{"text":"脚本记一笔"}' \
  https://tack.example.com/api/v1/capture
```

完整路由、scope、幂等和错误码见 [API 契约](docs/api.md)。

在「设置 → Agent 连接」为每个 Agent 创建独立 API Key，选择权限、项目/标签范围与有效期，并复制不含密钥的接入说明。当前使用 REST API，尚未提供 MCP 服务；详见 [Agent 连接指南](docs/agent-connections.md)。

## 文档

- [设计文档](Design.md)
- [API 契约](docs/api.md)
- [Agent 连接指南](docs/agent-connections.md)
- [运维手册](docs/runbook.md)
- [更新日志](CHANGELOG.md)
- [AI 协作入口](AGENTS.md)

AI 只做本地提交和本地 tag；推送和远端发布必须由用户明确确认。
