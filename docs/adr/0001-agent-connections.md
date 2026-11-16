# ADR 0001：Agent 连接复用 REST 与独立 API Key

日期：2026-11-17。状态：采用。

Tack 是单用户自托管看板，已具备 Bearer scope、项目/标签范围、sudo、原子 Agent 领取和 Codeg bridge。用户需要让其他 Agent 看懂如何连接，也需要独立撤销每个客户端的访问。

比较三个方案：只加静态文档最轻，但凭证管理与上手流程分离；API + 连接管理可直接复用现有安全边界；同时建设远程 MCP/OAuth 能为聊天客户端提供工具发现，但增加协议、授权发现、令牌刷新及多客户端验收工作。

采用 API +「Agent 连接」界面，连接就是命名的现有 API Token，不新增重复连接表。提供用途预设、资源范围、默认 30 天的可选到期、最近请求、撤销、只读凭证检查，以及不含秘密的 Agent 指南。旧 API/Token 保持兼容，新增字段可选，旧 Token 不过期。客户端检查用 Bearer 且省略浏览器 Cookie。

本次不提供 MCP endpoint，不宣称 REST URL 能作为 MCP Server 使用。有明确聊天客户端接入需求时，再以 REST 为边界添加 MCP 适配。授权按实际客户端选择；本地 stdio 可用环境凭证，远程授权须核对 MCP OAuth 规范及客户端支持，不能仅改路由名称。

官方依据（本次联网核对）：

- [GitHub fine-grained PAT](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens)：名称、有效期、资源选择和最小权限；适合借鉴 Key 管理流程。
- [Notion connections and tokens](https://developers.notion.com/guides/connections-and-tokens/overview)：内部连接/个人 Token 与多用户 OAuth 分开；能力与内容范围共同限制访问。
- [OpenAI MCP servers](https://developers.openai.com/api/docs/guides/tools-connectors-mcp)：MCP 是模型工具接入层，授权凭证仍需应用侧提供/管理。
- [MCP authorization](https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization)：区分 HTTP 授权流程与 stdio 环境凭证。这里引用稳定规范说明边界，未来实现时重新核对目标协议版本。
- [Claude custom connectors](https://support.anthropic.com/en/articles/11175166-about-custom-connectors-using-remote-mcp)：连接命名、认证方式、授予权限及移除连接；远程服务需从其云端可达。

后果：不新增运行依赖或授权系统，立即覆盖代码 Agent/脚本；仅接受 MCP 的聊天客户端暂不能直接接入。最近请求不被当作在线状态；配置说明可以分享，凭证必须独立保存。
