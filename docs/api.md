# HTTP 接口（单路原型）

默认地址：`http://127.0.0.1:18090`。

## 本机访问约束

`GET /health`、`GET /debug` 和页面静态资源可直接访问。摄像头、工具检查、WebRTC 信令和服务关闭接口要求请求头 `Authorization: Bearer <local_token>`。本机调试页自动取得并使用临时令牌；令牌随服务重启变化，不是 Java 后台的登录令牌。

接口要求 Host 与配置的监听地址匹配；存在 Origin 时必须与本机调试页同源。不提供跨域通配配置。此机制用于本机原型，不能替代后续设备级业务授权。接口不会返回 RTSP 密码。

## 已实现接口

| 方法 | 路径 | 结果 |
| --- | --- | --- |
| GET | `/health` | HTTP 200，`{"service":"localconverter","status":"ok"}`；仅表示控制服务可用 |
| GET | `/debug` | 本机摄像头调试页面，默认 `/` 也是该页面 |
| GET | `/tools` | FFmpeg、MediaMTX 是否存在，以及所属媒体进程是否运行 |
| POST | `/sessions` | 实际连接并等待媒体就绪；成功 HTTP 201，失败不返回假成功 |
| GET | `/sessions/{id}` | 查询会话和当前媒体状态 |
| POST | `/sessions/{id}/heartbeat` | 续租当前有效会话，过期会话不能复活 |
| POST | `/sessions/{id}/snapshot` | 原始尺寸 JPEG，最大 5 MiB，禁用缓存 |
| DELETE | `/sessions/{id}` | 幂等释放会话，HTTP 204 |
| POST | `/sessions/{id}/whep` | 转发 WebRTC/WHEP 协商，返回 SDP 及同源资源地址 |
| OPTIONS | `/sessions/{id}/whep` | WebRTC 能力查询 |
| PATCH / DELETE | `/sessions/{id}/whep/{resource}` | ICE 候选更新或关闭 WebRTC 媒体会话 |
| POST | `/shutdown` | 带本地凭证请求停止当前服务，HTTP 202 |

创建会话需要 `Content-Type: application/json`，请求示例：

```json
{
  "device_id": "camera-A",
  "config_version": 1,
  "rtsp_url": "rtsp://192.168.1.101:554/live"
}
```

设备 ID 必填，最长 128 字节；配置版本为非负整数。地址必须是 RTSP，端口有效，特殊字符需 URL 编码。请求限制 8 KiB，只允许文档中的字段。

成功返回示例（会话 ID 和时间仅为格式示意）：

```json
{
  "session_id": "随机会话ID",
  "device_id": "camera-A",
  "config_version": 1,
  "state": "ready",
  "expires_at": "2026-10-18T20:00:30+08:00",
  "preview_url": "/sessions/随机会话ID/whep"
}
```

同设备、同版本、同地址的请求共享底层连接，每个调用者拥有独立会话。仍有使用者时，关闭一个会话不影响其他人。配置改变且旧会话仍有效时返回 409，需关闭旧会话后重建；本原型尚未自动撤销所有旧会话或向后台核验版本。

默认每 10 秒心跳，租约 30 秒，最后一个会话释放后空闲 5 秒关闭底层源。期限、并发和媒体超时在示例配置中指定。服务不持久化设备清单，重启后需要重新申请会话。

## 错误

统一返回 JSON：`{"error":"中文原因"}`。

| 状态码 | 含义 |
| --- | --- |
| 400 / 415 | 字段、地址或请求格式错误 |
| 401 / 403 | 本地凭证无效、Host 或来源不匹配 |
| 404 | 会话不存在或过期 |
| 409 | 设备配置版本或连接参数冲突 |
| 413 | 截图超过大小限制 |
| 429 | 会话、连接或并行截图达到上限 |
| 502 | 媒体响应或截图失败 |
| 503 | 工具缺失、进程启动失败或媒体服务不可用 |
| 504 | 摄像头连接或截图超时 |

`ready` 表示收到源流，不承诺浏览器一定支持该编码。预览失败时页面显示实际错误，截图接口仍可单独验证。媒体失败不会自动回退到测试图片。
