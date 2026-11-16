# Tack Agent Entry

这是一个 Go + SQLite + React 的单用户任务看板。开工先读 `Design.md` 的相关章节；项目级决定以设计文档为准。

Baseline target: **Baseline 2024**（Chrome 115+ / Safari 18+ / Firefox 128+）。新浏览器能力先查 MDN；不在清单内的先问。

## 必读

- 每次开工：`git status`、`git log --oneline -10`，先理解现有实现。
- 涉及任务、认证、更新机制时，读 `Design.md` 对应章节，不要凭记忆改契约。
- 计划、草稿、agent 临时文件放 `.superpowers/`，不得入库。

## 命令

```bash
make dev       # Vite + Go 本地开发
make check     # 提交前必须全绿
make build     # 生成 bin/tack 和 bin/tack-updater
make docker-build
make docker-build-updater
./scripts/release.sh patch|minor|major  # 只准备本地提交/ tag，不 push
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

## 代码地图

- `cmd/tack/`：进程入口和 CLI 子命令
- `cmd/updater/`：更新 sidecar（Docker socket、备份、pull/recreate、回滚）
- `internal/api/`：HTTP 路由、中间件、DTO；不写领域规则
- `internal/auth/`：Argon2、Cookie session、API token scope
- `internal/capture/`：`#tag` / `@project` / `!pN` / `^日期`
- `internal/config/`：环境变量解析
- `internal/store/`：SQLite、迁移、查询
- `internal/tasks/`：任务状态、优先级、排序领域规则
- `internal/update/`：GitHub Release 检查、updater 客户端和状态
- `migrations/`：只追加的 SQL 迁移
- `web/`：React/Vite；`web/dist` 由构建生成并嵌入 Go 二进制

## 不变量

- SQLite、单用户、单板；不引入 ORM、Redis 或额外数据库容器。
- 时间统一 UTC RFC3339；迁移只增不改。
- 浏览器 Cookie 认证的写请求必须有 `X-Tack-Client: web`，并校验 Origin。
- 密码使用 Argon2 PHC；数据库只保存 session token 哈希。
- 生产 `TACK_PUBLIC_URL` 必须是 HTTPS；本地 HTTP 才允许非 `__Host-` Cookie。
- 前端 strict TypeScript，禁止 `any`；颜色只用 token。
- API Token 只能走 Bearer；Token 管理、Session、密码和更新只接受网页 Session。
- 更新链路由 app 检查 Release，由 updater 执行 Docker；app 不接触 docker.sock。
- updater 只接受 `vX.Y.Z`、必须能在 `TACK_GITHUB_REPO` 的 Release 中查到；更新前必须备份，失败必须恢复旧 tag 和数据库。
- `/api/v1` 只做兼容增量，破坏性变更开 v2。

## Git

- 提交：`type(scope): 中文描述`，正文 2–5 条。
- **AI 不自动 push、不推远端 tag、不 amend。**
- 用户明确说“推送 / 发布 / 推上去”后才执行远端操作。
- 一个提交只做一件事；功能或修复先写能复现的测试。
- 发布脚本只做本地 commit/tag；tag 推送后由 GitHub Actions 构建 GHCR 镜像和 Release。

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
- `go test ./...` 覆盖迁移升级、capture、token scope、幂等和任务生命周期。
