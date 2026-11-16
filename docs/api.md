# Tack API v1

当前为 M0 认证与运行状态契约。所有响应带 `X-Request-Id`；错误体：

```json
{
  "error": {
    "code": "invalid_credentials",
    "message": "用户名或密码错误",
    "request_id": "req_..."
  }
}
```

## 健康与版本

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/healthz` | 进程健康，不查数据库 |
| `GET` | `/readyz` | 数据库可连接 |
| `GET` | `/api/v1/version` | `{version, commit, build_time}`，无需认证 |

## 认证

| 方法 | 路径 | 说明 |
|---|---|---|
| `POST` | `/api/v1/auth/login` | `{username, password}`，成功设置 HttpOnly Session Cookie |
| `GET` | `/api/v1/auth/me` | 当前用户名；`sudo_expires_at` 在 M0 为 `null` |
| `POST` | `/api/v1/auth/logout` | 需要 Cookie、`X-Tack-Client: web` 和同源请求 |

浏览器请求由网页自动带 `X-Tack-Client: web`。后续 API token 在 M2 加入；更新和 token 管理在 M3。

## 本地 curl

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS -c /tmp/tack.cookies \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<local password>"}' \
  http://localhost:8080/api/v1/auth/login
curl -fsS -b /tmp/tack.cookies http://localhost:8080/api/v1/auth/me
```
