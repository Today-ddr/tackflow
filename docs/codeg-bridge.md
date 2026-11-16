# Codeg bridge CLI

`tack-bridge` 是独立的本地 worker，使用 HTTP 将 Tack 中手动标记 `agent_ready` 的 todo 任务交给 Codeg work_task，并将结果、评论和外链同步回 Tack。支持 macOS/Linux，不在 Tack app 内执行命令，也不读写 Codeg 数据库。安装或启动 Tack 不会自动启动 bridge。

## 配置和首次检查

1. 在 Tack「设置 → API 令牌」（`/settings/tokens`）创建专用令牌，选择 `read`、`write`，按提示输入当前密码。复制只显示一次的完整 `tk_...`。可限制项目/标签，队列会沿用该令牌的访问范围；`capture` 不需要。
2. 使用已启用 HTTP 服务的 Codeg 地址。Codeg server 使用 `CODEG_TOKEN` 作为访问令牌；bridge 的 `CODEG_API_TOKEN` 填同一个值。不要填 Tack token、代理模型的 API key 或 MCP 凭证。Codeg 的 proxy-jwt 模式也保留 Bearer 通道。
3. 选择 Codeg 中已有的根项目 folder ID。它不是 Tack 的 project ID，也不是路径。可在 Codeg HTTP API `POST /api/list_all_folder_details`（body `{}`）返回的 `id/path/parent_id` 中确认，选择 `parent_id:null` 的目标；不要使用 `/api/list_folders`，后者返回按会话聚合的路径信息，没有 folder ID。先在 Codeg 中配置该项目的默认 agent、权限及执行设置。
4. 将环境放在权限 `0600` 的本地文件或服务管理器配置中；不要提交凭证。

| 环境变量 | 要求 |
| --- | --- |
| `TACK_URL` | 必填，Tack 根地址，不带 `/api/v1` |
| `TACK_API_TOKEN` | 必填，具有 read/write scope 的专用 API Token |
| `CODEG_URL` | 必填，Codeg HTTP 根地址，不带 `/api` |
| `CODEG_API_TOKEN` | 必填，对应 Codeg 的 HTTP access token |
| `CODEG_PROJECT_ID` | 必填，Codeg 根项目 folder 的正整数 ID |
| `TACK_BRIDGE_WORKER` | 必填，稳定的 worker 标签，最多 100 字节 |
| `TACK_BRIDGE_STATE` | 必填，本地持久状态 JSON 路径 |
| `TACK_BRIDGE_POLL_INTERVAL` | 默认 `10s`，范围 `1s`–`1m` |
| `TACK_BRIDGE_HTTP_TIMEOUT` | 默认 `15s`，范围 `1s`–`1m`，包含读取响应体 |
| `TACK_BRIDGE_RESULT_STATUS` | 默认 `review`；仅显式设置 `done` 时允许成功结果直接完成 |

两个 URL 都无默认值。仅 loopback/localhost 允许 HTTP；其他地址必须 HTTPS。不接受 URL 用户名、密码、query 或 fragment。支持反向代理的路径前缀。两个服务分别使用 Bearer，bridge 不使用 Cookie。

```sh
go build -o bin/tack-bridge ./cmd/tack-bridge

# 先注入上述环境变量，再执行检查。
bin/tack-bridge --check
bin/tack-bridge --once
bin/tack-bridge
```

`--check` 调用 Tack `GET /api/v1/agent/next`，Codeg `POST /api/health`、`POST /api/get_folder` 和 `POST /api/work_task_list`。它校验配置、read 认证、项目是否为根 folder 及队列 API 可达，不创建状态文件、不 claim/create/start。它不能在不写入的情况下证明 Tack write scope、Codeg agent 能启动或底层项目权限正常。

`--once` 执行一个有限周期：最多 claim 一张任务，恢复/创建映射并启动或读取一次 Codeg 状态。活跃任务保存在状态文件；它不会一直等到任务结束。默认单周期截止时间为 `10 × HTTP_TIMEOUT`（150 秒），每次 HTTP 另有限时。持续模式在周期后等待 poll interval，响应 SIGINT/SIGTERM。任一错误退出非零，修复后使用**相同状态文件及配置**重启；不内置无界失败重试。已启动任务不因 bridge 退出自动取消。

## 真实 Codeg 协议

下表依据本地只读源码 `/Users/dayto/Desktop/P/codeg`，核对 commit `8710e9375`（2026-11-16）。这些是服务器 HTTP 命令，不是 agent MCP 工具。路由前缀 `/api`，全部 POST，成功响应没有 `data` 包装。

| 目的 | 路由 | 请求 JSON | 成功响应 |
| --- | --- | --- | --- |
| 健康检查 | `/api/health` | `{}` | `{status:"ok",version:"..."}` |
| 校验项目 | `/api/get_folder` | `{folderId:7}` | `FolderDetail`，包含 `id,parent_id` |
| 查项目列表 | `/api/list_all_folder_details` | `{}` | `FolderDetail[]`，仅配置时手动使用 |
| 查任务/恢复创建 | `/api/work_task_list` | `{folderId:7}` | `WorkTaskInfo[]` |
| 创建 | `/api/work_task_create` | `{draft:{folder_id:7,title:"...",config:{...}}}` | `WorkTaskInfo` |
| 查询 | `/api/work_task_get` | `{id:42}` | `WorkTaskInfo` |
| 启动 | `/api/work_task_start` | `{id:42}` | JSON `null` |

注意外层 `folderId` 是 camelCase，而 draft/response 的 `folder_id`、`prompt_blocks` 等是 snake_case。config 包含文本 `prompt_blocks:[{type:"text",text:"标题与备注"}]`、`display_text` 和持久化恢复标记 `tack_bridge_marker`。标题和备注只经 JSON 编码，bridge 不 shell 插值、不执行任务标题。

精确源码证据（相对于上述 Codeg checkout）：

- `src-tauri/src/web/router.rs:54` 健康路由；`:302`–`:313` folder 路由；`:1780`–`:1811` work_task 路由；`:2218` `/api` 挂载；`:2269` health 响应。
- `src-tauri/src/web/auth.rs:38` `Authorization: Bearer` 匹配；`:58` 空 token 拒绝；`:69` proxy-jwt 保留 token。
- `src-tauri/src/web/handlers/work_task.rs:20` ListParams、`:27` IdParams、`:41` CreateParams；`:217` list、`:227` get、`:256` create、`:307` start 裸 JSON 响应。
- `src-tauri/src/models/work_task.rs:9` WorkTaskInfo；`:128` WorkTaskDraft 无 forge source 字段；`:187` WorkTaskConfig。`src-tauri/src/db/service/work_task_service.rs:179` list 排除已删除任务/目录、包含其他状态；`:491` 校验 prompt；`:502` create 校验根 folder 并原样保存 config，非 forge create 无幂等键。
- `src-tauri/src/commands/work_task.rs:223` create 后 nudge scheduler；`:436` start 调用 engine；`src-tauri/src/work_task/engine.rs:763` start 是 todo→queued 的 CAS，不能当成无条件重试接口；`:1241` fresh launch 必须有 prompt_blocks。
- `src-tauri/src/db/entities/work_task.rs:17` 实际状态枚举；`:96` verdict 为 `success/needs_review/blocked`。`success` 和 `needs_review` **不是** HTTP task status。
- `src-tauri/src/web/handlers/folders.rs:14` FolderIdParams、`:34` get_folder；`src-tauri/src/models/folder.rs:16` FolderDetail；`src-tauri/src/models/conversation.rs:237` list_folders 的 FolderInfo 无 ID。
- `src/components/workspace/deep-link-bootstrap.tsx:12` 真实会话链接 `/workspace?folderId=X&conversationId=Y&agent=Z`；`src-tauri/src/work_task/engine.rs:1622` 将任务会话绑定 worktree folder。外链有会话时使用该链接及 worktree folder ID；没有会话时回到 `/workspace`，评论和 external_id 保留准确 work_task ID，不捏造任务详情 URL。

升级 Codeg 后应重新核对这些协议及测试 fixture。

## 租约、状态和结果

Tack claim 是 `POST /api/v1/agent/claim {worker}`，返回 `{task,claim:{id,expires_at}}`；只有有效的 `doing + agent_ready` 响应才继续。`GET /agent/next` 只用于 `--check`。空队列不调用 Codeg create/start。

每个活跃周期及 create/start/result 前以 `POST /api/v1/agent/tasks/{id}/heartbeat {claim_id}` 验证并续租；期望 `{claim:{id,expires_at}}`。Tack 租约为 30 分钟。默认空闲轮询间隔 10 秒，单次 HTTP 最长 15 秒。心跳失败、旧 claim、权限撤销、过期或合同不匹配都停止，不生成新的结果请求。

| Codeg 状态 | bridge 行为 |
| --- | --- |
| todo | 仅首次已确认映射时 start，先持久化 start 意图 |
| queued / preparing / running / awaiting_input / merging | 保持续租和监控；有成功 verdict 也不会提前回写 |
| review / done | 默认回写 Tack review；显式设置 done 时才可回写 done，blocked verdict 仍 review |
| failed / canceled | 回写 review，评论包含状态、verdict、failure_reason、last_error、result_summary；不自动 retry/requeue |
| 未知状态 | 报错停止，保留映射供核对协议 |

回写 `POST /api/v1/agent/tasks/{id}/result`，携带 claim_id、status、comment 和 `{kind:"codeg_work_task",external_id:"42",url:"..."}`。完整结果及随机 `Idempotency-Key` 先落盘，响应成功后才标记完成；评论截取最多 4000 字符并去除已配置的两端 token。请求/服务端错误日志不输出 URL、响应体、token 或任务文本。

Tack 必须保证同一 claim、同一结果的原子幂等，以及旧 claim 拒绝。result 响应丢失后，重启直接重放保存的**完全相同**请求及 key，即使旧 heartbeat 已失效也不生成新结果；由 Tack 验证这是已完成结果的重放还是过期/失效请求。普通结果生成仍先 heartbeat。轮换 Tack token 会改变 owner，活动租约不能迁移给新 token。

## 持久化与不确定结果

状态 JSON 使用 `0600`，新建父目录 `0700`，保存采用临时文件、fsync、原子 rename 和目录 fsync。同目录 `.lock` 用 OS flock 保证一份状态同时只有一个进程使用，进程退出即释放；不要删除 lock 文件。状态绑定两端 URL、folder ID、worker；换目标不能复用旧状态。损坏/未知版本/超限状态直接报错，不当成空状态。

状态包含 claim、原任务文本、创建/启动阶段、Codeg ID、待回写结果、completed 映射，不存配置中的 API token。它仍包含敏感任务和 claim，请同凭证一起保护、备份。状态上限 16 MiB；HTTP 响应上限 4 MiB，请求上限 1 MiB；不跟随 HTTP 重定向。禁用 HTTP 客户端隐式请求体重放，由持久状态决定何时重试。

创建前先查项目任务列表中的 `config.tack_bridge_marker`（由规范化 Tack URL 和 task ID 的 SHA-256 派生）。真正发送 create 前将 `phase:creating` 及 claim 持久化。以下任何阶段失败都保留信息：

- **返回明确任务 ID**：验证 folder/marker 并落盘，再启动/监控。
- **创建超时、断连、无效响应或写盘前进程退出**：保存的阶段必须为 `creating`，下次只查 marker。一个匹配恢复映射；多个匹配报错；没有匹配也报 `ambiguous Codeg create`，绝不盲目重发。
- **尚无创建意图却查到 marker**：`claimed` 阶段发现既有 Codeg 任务，可能来自回滚状态、另一份状态或旧执行；报 `without a persisted create intent` 并停止，不接管它或把其结果写回。
- **发送 start 后不确定**：下次先 get。已经运行或终结则继续；仍 todo 则停止，由人确认，避免错误地启动一次被取消/重排的任务。
- **领取响应不确定**：预先保存 claim key，30 分钟内重放同一个 claim 请求；超出时间窗口停止，避免幂等缓存失效后意外领走另一张任务。
- **已完成映射**：保留 Tack→Codeg ID 作为完成记录。之后再次领到这个 task ID，报 `completed mapping` 并给出 ID，保留领取请求 key 后停止；不创建第二份，不把旧 Codeg 结果写给本次领取。旧状态如果同时把这个 ID 记为 active 和 completed，也在加载时拒绝运行。

`work_task_create` 没有通用 idempotency/external unique source，forge 专用 source 不允许普通 draft 伪造。marker 和列表恢复不是跨多个独立状态文件的分布式 exactly-once。一个目标队列应由一个持久 worker 管理，不能通过复制/丢弃状态或更改 URL 别名绕过恢复保护。列表不包含被删除的 Codeg 任务，编辑 config 也可能抹掉 marker，这些情况需要人工处理。

出现 ambiguous/expired 时：停止 worker，保留并备份状态；从中读取 marker、task ID、claim、phase，使用上述只读 list/get 查看 Codeg，并在 Tack 检查该任务是否已人工改动/被其他 worker 领取。若远端匹配稍后出现，修复网络后用原状态重试即可；若无匹配，须确认原请求不再执行、Codeg 任务未被删除/改写，再人工决定接续或把 Tack 卡移到 review。不要将状态清空当作重试。bridge 不取消、删除或自动重做未知任务，也不替人工作此决定。

## 再次执行与备份回滚

当前设计是在同一份持久历史内，每张 Tack 卡最多自动创建一个 Codeg work_task。**修改内容后需要新一轮执行，请新建 Tack 卡，再设置 todo 和 agent_ready。** 把已完成卡重新移到 todo 并开启 agent_ready 不表示新的执行版本。标题或备注相同也不能证明旧结果适用于本次要求，因此 bridge 不通过比对内容自动放行。

若误操作已触发 `completed mapping`：领取接口可能已经把旧卡变为 doing，bridge 此时没有执行新的 Codeg 写入或结果回写。先停止 bridge、备份状态，在 Tack 中核对报错的 ID，将旧卡关闭 agent_ready 并移回人工确认的 review/done，使该领取失效。只有核实此次停止来自这个完成记录冲突、状态没有 active、旧领取已经失效后，才可人工清除状态中的 `claim_key` 和 `claim_started_at`，保留 completed 及目标绑定，再用相同状态启动以处理新卡。其他 ambiguous/create/start/result 错误不适用这个清理步骤；不要删除整个状态或 completed 记录。

Tack 的持久递增 ID 能防止正常回收站清理后复用 ID，但恢复旧数据库也会恢复旧的 ID 序列，不能证明外部 Codeg 历史已回滚。保留较新的 bridge 状态时，重复 ID 会被 completed 保护拒绝；如果 bridge 状态回退到创建之前，查到既有 marker 也会停止。数据库、bridge 状态和 Codeg 执行没有跨系统回滚事务。恢复备份前停止 worker，保留恢复前后的状态与映射，逐项核对 Codeg 的 list/get 后再恢复运行。若状态历史与远端 marker 同时丢失/删除，客户端不能重建缺失的执行身份或提供 exactly-once 保证；不得把重置状态作为自动恢复方案。

已经保存的 result_pending 仍只重放原请求。停机超过服务端租约/去重允许的时间后，可能得到 409 并要求人工核对；不承诺无限期结果重放，不自动重新派发。

## 验证

```sh
go test -race ./internal/bridge ./cmd/tack-bridge
go vet ./internal/bridge ./cmd/tack-bridge
golangci-lint run ./internal/bridge/... ./cmd/tack-bridge/...
make check
```

测试使用临时私有状态和 `httptest.Server`，Codeg fixture 对齐上述实际 DTO/路由/认证。覆盖空队列、正常完成、重启、丢失 create/start/result/claim 响应、唯一/无/多 marker、活跃状态续租、心跳失败、过期、失败原因、凭证不泄漏、重定向、响应上限、取消、文件锁与坏状态。另用真实 Tack API 和临时 SQLite 验证修改内容后手工 requeue、恢复旧数据库导致 ID 重复均不会回写旧结果；覆盖回退 bridge 状态与旧版 active/completed 冲突。实现验证不向正在运行的 Codeg 或用户真实 Tack 数据派发任务。
