# Changelog

<!-- git-cliff: end of header -->

## [0.2.0] - 2026-11-16

首个可日常使用的版本。单用户、单看板、手机收集、脚本 API 和网页内更新链路已经打通。

### 新增

- 看板：Inbox / 待办 / 进行中 / 待验收 / 已完成五列，支持拖拽排序、键盘移动和乐观更新回滚。
- 快速收集：`#标签`、`@项目`、`!p0`–`!p3`、`^今天/明天/后天/日期` 内联语法，手机全屏 capture 页。
- 项目、标签、任务详情、时间线、评论、软删除和 30 天回收站清理。
- API v1：任务 CRUD/move/comments/events、capture、项目、标签、设置、导出、版本和更新状态。
- 认证：Argon2 密码、滑动 Session、5 分钟 sudo、API Token scope、幂等键和 cursor 分页。
- 更新：GitHub Release 检查、可调频率、侧栏红点、Release notes、updater sidecar、备份、健康检查失败自动回滚。
- PWA：standalone manifest、192/512 图标、手机底栏和安全区适配。
- 数据：SQLite WAL、每日备份、JSON/CSV 导出、时区注入和容器健康检查。
- 发布：多架构 GHCR 镜像、Docker Compose updater、本地 release 脚本和 GitHub Release 工作流。

### 修复

- 新建 API 令牌现在可以直接用当前密码完成 sudo，不需要先打开另一个验证弹窗。
- updater 先改镜像 tag 再 pull，避免拉取旧版本；失败时恢复 `.env`、数据库和旧容器。
- 恢复更新前数据库时保留原文件权限，避免 updater 以 root 写入后 app 无法继续写 SQLite。

### 文档

- 补齐 API 契约、部署/升级/回滚手册、AI 协作入口和代码地图。

## [0.1.0] - 2026-11-16

### 新增

- M0 工程骨架：Go 单二进制、SQLite 迁移、登录 Session、健康检查和嵌入式前端。
- Docker Compose + Caddy 本地部署骨架。
- React/Vite 暖白纸张风格登录页与空看板外壳。
- GitHub Actions 质量门。
