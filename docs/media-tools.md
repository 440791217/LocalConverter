# 媒体依赖

单路原型使用 MediaMTX v1.21.2 和 FFmpeg 9.0.2 Windows amd64 便携版本。

- MediaMTX 官方发布：https://github.com/bluenviron/mediamtx/releases/tag/v1.21.2
- FFmpeg 官方下载入口：https://ffmpeg.org/download.html
- Windows 构建提供方：https://www.gyan.dev/ffmpeg/builds/

执行 `scripts/install_media.ps1` 安装到工程 `tools` 目录。脚本校验已核对的包 SHA256，不修改系统 PATH。当前电脑已安装；`-Force` 可以重新下载并校验。FFmpeg 下载别名未来可能指向新版本，校验不符时脚本会拒绝安装，维护者需审核新版本及校验值后更新。

`tools` 不提交 Git。MediaMTX 的 MIT 许可证和 FFmpeg 构建附带的许可证保存在各工具目录。浏览器 reader.js 来自同一 MediaMTX 版本，许可证保存在 `internal/api/web/MediaMTX-LICENSE.txt`；本项目修改了资源续传及关闭请求，使它们携带与创建时相同的本地认证头，并在关闭时释放 WHEP 会话。

部署包包含第三方二进制时，需保留相应许可证并按所选构建许可履行分发义务；本仓库不把工具二进制作为自行编写的 Go 代码发布。
