# mdc-go

本项目受 [mdcng](https://github.com/mdc-ng/mdc-ng) 和 [amane](https://github.com/sqzw-x/amane) 启发。mdcng 提供了整理流程、文件名垃圾信息过滤等思路和默认配置，amane 提供了番号识别规则的思路。mdc-go 是用 Go 重新实现的版本，代码没有直接复制，番号规则在 `internal/server/number_rules.go` 中按 Go 重写，并在注释中标注了参考来源。

mdc-go 监控本地媒体目录，为每个视频刮削元数据和图片，生成 NFO 和海报、封面、背景图，并可以把结果整理到目标目录，供 Emby、Jellyfin 等媒体服务器使用。

## 功能

- **监控与刮削**：监控指定目录，发现新视频后自动识别番号，从数据源抓取标题、演员、标签、简介、封面和海报等信息。
- **番号识别**：内置多种番号格式规则，包括 FC2、HEYZO、Tokyo-Hot、DMM 连写和素人数字前缀等。识别前会去掉文件名中的垃圾信息（默认规则来自 mdcng，可在设置中修改）。
- **数据源插件**：数据源以插件形式存在，内置 avbase、javdb、javdbui、Javbus、dmm 等。插件由 `plugin.json` 和 `main.js` 组成，可以在页面上安装、导出和删除。数据源可以设置优先级，并支持覆盖网址。
- **图片处理**：支持海报裁剪（左、中、右、人脸定位），可以选择图片来源，并可以添加字幕、分辨率等水印。
- **整理方式**：支持移动文件、原地整理和生成 STRM 三种模式。
- **批量操作**：批量停止、删除和重试任务，在后台执行并显示进度。
- **演员映射**：可自定义演员名映射表，启动时会检查映射表更新。
- **Emby 刷新**：刮削完成后可以通知 Emby 刷新媒体库。
- **Web 界面**：内嵌在程序中，无需单独部署前端。

## 运行

### Docker（推荐）

仓库根目录已提供 `docker-compose.yml`，按需修改其中的目录挂载后执行：

```bash
docker compose up -d
```

访问 `http://<服务器地址>:19208` 打开页面。

容器内的挂载路径就是页面中填写的监控目录和整理目录。例如把宿主机的 `/mnt/media` 挂载到容器的 `/media`，页面中就填写 `/media/...`。

### 从源码编译

需要 Go 1.26 或更新版本。程序不依赖 CGO。

```bash
go build -o mdc-go ./cmd/mdc-go
./mdc-go
```

默认监听 `0.0.0.0:19208`。可用的启动参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-listen` | `0.0.0.0:19208` | HTTP 监听地址 |
| `-data` | 程序目录下的 `data` | 配置、数据库、日志、演员映射表 |
| `-plugins` | 程序目录下的 `plugins` | 数据源插件目录 |

首次启动时，程序会在 `data` 和 `plugins` 目录中生成默认配置和内置插件。

### 依赖

- `ffprobe`（来自 ffmpeg）：用于识别视频的真实分辨率。Docker 镜像已包含；从源码运行时需要自行安装。没有 `ffprobe` 时，分辨率识别会失败，但不影响其他功能。

## 数据源插件

插件目录结构如下：

```
plugins/
  javdb/
    plugin.json   # 插件信息：ID、名称、版本、默认地址
    main.js       # 抓取逻辑
    config.json   # 运行时设置（启用状态、地址、Cookies 等）
```

- 上传插件时只接受根目录的 `plugin.json` 和 `main.js`。`config.json` 由程序在运行时保存，内置插件目录中才有默认的 `config.json`。
- 插件地址默认取 `plugin.json` 中的 `base_url`，`config.json` 中保存的 BaseURL 优先。
- 版本号不同时，程序才会用内置插件覆盖已安装的同名插件。

插件的编写方法、可用接口和错误码见 [插件开发文档](docs/plugin-development.md)。

## 目录说明

```
cmd/mdc-go/          程序入口
internal/server/     HTTP 接口、刮削流程、番号识别、图片处理、整理与批量任务
internal/provider/   插件加载、运行时与优先级
internal/provider/defaults/   内置插件
internal/web/        Web 前端（编译进程序）
```

## 开发

```bash
go vet ./internal/server/ ./internal/provider/
go test ./internal/server/ ./internal/provider/ -count=1
```

开发时可以用 `-assets` 参数直接读取磁盘上的前端文件，修改后刷新页面即可生效，不需要重新编译。

## 许可证

本项目以 [GNU General Public License v3.0](LICENSE) 发布。

Copyright (C) 2026 todoxu
