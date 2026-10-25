# LocalConverter

独立的工位摄像头采集服务，使用 Go 标准库提供控制接口，MediaMTX 提供 RTSP 接入与 WebRTC 预览，FFmpeg 提供原始尺寸 JPEG 截图。

当前已完成单路原型：真实摄像头接入、浏览器预览、截图、会话共享、心跳、空闲释放、基本并发限制和媒体进程回收。尚未接入现有 SOP 标注页面及 Java 后台签发的业务权限凭证。

## 快速开始

在项目根目录运行：

```powershell
# 当前电脑的便携媒体工具已经准备好；其他电脑先安装依赖。
.\scripts\install_media.ps1
.\scripts\start.ps1
```

打开 `http://127.0.0.1:18090/debug`，填写设备 ID、配置版本和完整 RTSP 地址，点击“连接并预览”，然后拍照或关闭连接。地址可以包含经过 URL 编码的用户名、密码；调试页不会将地址写入本地存储或业务数据库。

停止：终端中按 Ctrl+C，或从另一终端执行：

```powershell
.\scripts\stop.ps1
```

这是本机调试入口，不能直接对公网或局域网开放。接口使用启动时生成的本地临时令牌，只接受匹配的请求 Host 和网页来源；现有业务前端跨来源访问、HTTPS 接入、后台采集授权将在下一阶段实现。

## 开发和构建

```powershell
go run ./cmd/localconverter -config configs/localconverter.json
go build -o bin/localconverter.exe ./cmd/localconverter
```

`-listen 127.0.0.1:18091` 可以覆盖 JSON 中的控制接口地址。只有控制接口端口改变，媒体端口仍由 JSON 配置决定，因此不要同时运行使用相同媒体端口的多个实例。

Go 不在当前终端 PATH 中时，重新打开终端，或使用本机路径 `D:\Program Files\Go\bin\go.exe`。

采集服务只做 H.264 直通，保持源分辨率与源帧率，不提供限帧或预览重编码。刷新帧率和检测帧率统一在业务系统配置中维护，摄像头可覆盖公共参数。详见 [网络与预览配置](docs/network-integration.md)。

## 代码划分

```text
cmd/localconverter/main.go     读取配置，组装服务、会话管理和媒体适配
internal/config/              JSON 配置和启动校验
internal/api/                 本机调试页、HTTP 接口、临时认证与 WebRTC 信令代理
internal/camera/              会话/连接管理、MediaMTX 控制、FFmpeg 截图、进程保护
configs/                      不含摄像头凭据的示例配置
scripts/                      安装便携媒体依赖、启动和停止
 docs/                        设计、接口、部署与依赖说明
 tools/                       媒体工具，不提交 Git
 bin/                         Go 编译产物，不提交 Git
 .cache/                      本地构建和媒体运行缓存，不提交 Git
```

日志统一写入 `D:\projects\logs`，文件名前缀为 `localconverter`。不会记录完整认证地址或访问令牌。进程只回收本服务启动的媒体工具，不按软件名称批量终止其他程序。

## 文档

- [总体设计和当前实现状态](docs/design.md)
- [实际 HTTP 接口](docs/api.md)
- [启动、端口和故障排查](docs/deployment.md)
- [媒体版本、来源和安装](docs/media-tools.md)

## 验证

```powershell
go vet ./...
go test ./...
# 安装媒体工具后，额外运行真实工具链测试，使用明确的合成视频源。
$env:LOCALCONVERTER_MEDIA_TEST = '1'
go test ./internal/camera -run TestRealMediaPipeline -count=1 -v
```

测试视频源只存在于验证过程中，不会被当作真实摄像头或在失败时充当替代画面。浏览器无法解码某些摄像头编码时，先选用兼容的 H264 子码流；本阶段不自动执行长期转码。
