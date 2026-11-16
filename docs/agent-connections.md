# Agent 连接

Tack 已支持 REST API，提供任务查询、快速收集、任务写入和原子领取/续租/结果回写。能执行 HTTP 请求的代码 Agent、脚本和快捷指令可以直接使用；当前没有 MCP 服务，不能把 `/api/v1` 填入 MCP Server URL。

## 建立连接

1. 登录「设置 → Agent 连接」（`/settings/connections`，旧 `/settings/tokens` 也有效），为每个 Agent 单独命名 Key。
2. 选择用途：只读助手用 `read`，仅收集用 `capture`，执行任务用 `write`（已包含 read/capture，也能删除授权范围内任务）。可组合自定义权限。
3. 限定项目/标签，选择有效期。默认 30 天，可选 7/90/365 天或不过期；两组范围都选时须同时满足。创建需最近 5 分钟内验证密码。
4. 保存仅展示一次的 Key，在 Agent 的秘密管理功能或运行环境变量 `TACK_API_TOKEN` 中配置。不要放进任务正文、聊天提示、仓库、URL 或日志。页面不把 Key 存到 localStorage、URL 或 Query/Mutation 状态缓存。
5. 点击对应 Key 的「接入说明」，复制给 Agent。说明包含 API 地址、当前 scope/资源 ID 和分类名称、操作步骤与错误处理，不含 Key。

## 检查与使用

网页「检查连接」固定向当前 Tack 发 GET，只使用填写的 Bearer Key，省略 Cookie、不跟随重定向，也不创建/领取任务。检查后清空输入。网页显示认证通过仅证明浏览器到 Tack 的凭证可用；Agent 仍须在自己的运行环境执行：

```bash
curl --fail-with-body --silent --show-error \
  'https://tack.example.com/api/v1/agent/connection' \
  -H "Authorization: Bearer $TACK_API_TOKEN"
```

将示例域名换成页面给出的实际地址。`localhost` 是调用方自己的机器；远程 Agent 需要可达的 HTTPS 地址。成功应为 `auth_kind=token`，返回此 Key 的 scope、范围和到期时间。401 表示无效、已撤销或已过期；403 表示权限不足；范围外资源返回 404。

- 只读：`GET /tasks` / `GET /tasks/{id}`，按 `next_cursor` 读完分页。
- 收集：`POST /capture {text}`；限定范围时须包含授权的已有 `@项目` / `#标签`。capture-only Key 不能列分类，复制说明会提供选定的分类名称；包含空格等无法以内联语法表达的名称需调整分类或改用 write + 数字 ID 建卡。
- 执行：先由用户将任务设为 `todo + agent_ready`，`POST /agent/claim` 领取并保存 claim id，每 10 分钟 heartbeat，30 分钟租约内 result 默认回到 `review`。收到 409/401 停止，不覆盖人工修改。
- 写操作应持久保存 `Idempotency-Key`，同一操作重试沿用相同 key/body；结果不确定或收到 `409 idempotency_uncertain` 时，有 read/write 权限才查询现状。仅 capture 的 Key 应停止自动恢复，保留原 key/body，请用户在网页核实；不能换 key 盲重试或为恢复操作扩大权限。

完整契约见 [API v1](api.md)。Codeg 自动派发使用独立 [tack-bridge](codeg-bridge.md)，页面不会启动 Agent。

## 维护

最近请求是 Key 的使用记录，含连接检查；不表示 Agent 在线。已过期 Key 仍留在列表，可以撤销。权限范围创建后固定；轮换时先生成新 Key，在 Agent 中替换并验证，再撤销旧 Key。进行中的领取绑定旧 Key，应先完成工作再轮换，避免旧领取无法回写。丢失明文时创建新 Key，不尝试恢复旧 Key。
