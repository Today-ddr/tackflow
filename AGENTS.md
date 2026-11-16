# Tack Agent Entry

这是一个 Go + SQLite + React 的单用户任务看板。开工先读 `Design.md` 的相关章节；项目级决定以设计文档为准。

## 必读

- 每次开工：`git status`、`git log --oneline -10`，先理解现有实现。
- 涉及任务、认证、更新机制时，读 `Design.md` 对应章节，不要凭记忆改契约。
- 计划、草稿、agent 临时文件放 `.superpowers/`，不得入库。

## 命令

```bash
make dev       # Vite + Go 本地开发
make check     # 提交前必须全绿
make build     # 生成 bin/tack
make docker-build
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

## 代码地图

- `cmd/tack/`：进程入口和 CLI 子命令
- `internal/api/`：HTTP 路由、中间件、DTO；不写领域规则
- `internal/auth/`：Argon2、Cookie token、session
- `internal/config/`：环境变量解析
- `internal/store/`：SQLite、迁移、查询
- `migrations/`：只追加的 SQL 迁移
- `web/`：React/Vite；`web/dist` 由构建生成并嵌入 Go 二进制

## 不变量

- SQLite、单用户、单板；不引入 ORM、Redis 或额外数据库容器。
- 时间统一 UTC RFC3339；迁移只增不改。
- 浏览器 Cookie 认证的写请求必须有 `X-Tack-Client: web`，并校验 Origin。
- 密码使用 Argon2 PHC；数据库只保存 session token 哈希。
- 生产 `TACK_PUBLIC_URL` 必须是 HTTPS；本地 HTTP 才允许非 `__Host-` Cookie。
- 前端 strict TypeScript，禁止 `any`；颜色只用 token。
- `/api/v1` 只做兼容增量，破坏性变更开 v2。

## Git

- 提交：`type(scope): 中文描述`，正文 2–5 条。
- **AI 不自动 push、不推远端 tag、不 amend。**
- 用户明确说“推送 / 发布 / 推上去”后才执行远端操作。
- 一个提交只做一件事；功能或修复先写能复现的测试。

## 质量门

`make check` 必须全绿：

- `gofmt` 无输出
- `go vet ./...`
- `golangci-lint run ./...`
- `go test ./...`
- `pnpm -C web lint`
- `pnpm -C web typecheck`
- `pnpm -C web test -- --run`
- `pnpm -C web build`
