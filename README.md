# Tack

Tack（图钉）是一个单用户、单看板的自建任务收集器。手机可以先记一笔，桌面可以拖卡，所有数据留在自己的 SQLite 文件里。

当前状态：**M0 骨架**。已经有登录、Session、健康检查、Docker/Caddy 骨架和 CI；任务 CRUD、拖拽和 capture API 在 M1。

## 本地开发

```bash
pnpm -C web install --frozen-lockfile
make dev
```

`make dev` 会在 `deploy/.env` 不存在时生成一份本地环境，启动 Vite（<http://localhost:5173>）和 Go API（<http://localhost:8080>）。Vite 会把 `/api` 代理到 Go。

## 质量门

```bash
make check
```

包含 `gofmt`、`go vet`、`golangci-lint`、Go 测试、前端 lint/typecheck/test/build 和单二进制构建。

## Docker

```bash
# 先编辑 deploy/.env，至少替换密码、secret 和 GHCR owner
make docker-build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

浏览器打开 `TACK_PUBLIC_URL`，首次启动会使用 `TACK_ADMIN_PASSWORD` 创建唯一的管理员。生产环境请使用 HTTPS 域名；本地 HTTP 会自动使用非 `__Host-` Cookie。

## 文档

- [设计文档](Design.md)
- [API 契约](docs/api.md)
- [运维手册](docs/runbook.md)

发布和更新链路在 M3；当前只做本地构建与部署骨架，不执行 push 或远端 tag。
