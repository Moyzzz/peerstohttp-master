# peerstohttp

[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp?ref=badge_shield)
[![Go Report Card](https://goreportcard.com/badge/github.com/WinPooh32/peerstohttp)](https://goreportcard.com/report/github.com/WinPooh32/peerstohttp)

一个简单的种子代理：把 BitTorrent 种子转成 HTTP 流，并通过 REST 风格的 API 控制。

基于 [anacrolix/torrent](https://github.com/anacrolix/torrent) 库构建。

> **本地部署与启动说明见 [DEPLOY.md](DEPLOY.md)**：覆盖 Windows / Linux / Docker Compose 三种方式，含编译依赖、启动参数、验证清单与常见问题排查。

## 文档

| 文档 | 面向 | 内容 |
|---|---|---|
| [docs/architecture.md](docs/architecture.md) | 开发者 | 分层结构、`/content` 完整数据流、存储与持久化、全局约束 |
| [docs/torrent-lifecycle.md](docs/torrent-lifecycle.md) | 开发者 | 种子生命周期：引用计数、两段式回收、两个定时器、锁顺序、失败路径 |
| [docs/content-streaming.md](docs/content-streaming.md) | 开发者 | 读取链路、读取取消的实现与成因、Range/206、排障日志 |
| [docs/API.md](docs/API.md) | 接口使用方 | 三个 HTTP 端点、路径参数、响应格式、状态码 |
| [docs/OPS.md](docs/OPS.md) | 运维 | 启动方式、全部 23 个启动参数、端口与防火墙、验证与排查 |
| [docs/releases/v0.2.0.md](docs/releases/v0.2.0.md) | 所有人 | v0.2.0 版本说明（最新） |
| [docs/releases/v0.1.0.md](docs/releases/v0.1.0.md) | 所有人 | v0.1.0 版本说明 |

> **本项目是 [WinPooh32/peerstohttp](https://github.com/WinPooh32/peerstohttp) 的非官方衍生版本（unofficial derivative）**
>
> 原始项目版权归原作者所有，按 [GNU GPL v3.0](LICENSE) 授权发布。本仓库在上游基础上修复了新版工具链下的编译问题，并补充了部署文档与启动脚本；本仓库的修改部分同样以 GPL-3.0 发布，完整条款见 [LICENSE](LICENSE)。
>
> *This is an unofficial derivative of the upstream project. Original copyright belongs to its authors; this fork and its modifications are released under GPL-3.0 as well.*

与上游相比的主要改动：

| 类别 | 内容 |
|---|---|
| 编译兼容 | `vendor/github.com/anacrolix/go-libutp/utp_types.h`：用 `__STDC_VERSION__ < 202311L` 条件编译包裹 `typedef uint8 bool;`，兼容 GCC 15+（默认 `-std=gnu23` 下 `bool` 成为关键字） |
| 编译兼容 | `vendor/github.com/anacrolix/go-libutp/` 下的 `callbacks.go`、`utp.go`、`socket.go`：把 7 个定义在 cgo 类型别名上的方法改为普通函数，兼容 Go 1.21+ |
| 文档 | 新增 `DEPLOY.md`：Windows / Linux / Docker 部署手册，含参数速查、验证清单、错误对照表，第 10 章为依赖补丁附录 |
| 脚本 | 新增 `start.ps1`：Windows 启动脚本，从 `-MingwBin`、`PEERSTOHTTP_MINGW_BIN`、PATH 上的 `gcc`、`CC`、`MSYS2_ROOT` 探测工具链，不再写死路径 |
| 配置 | 修正 `docker-compose.yml`：镜像名改为小写、去掉 `network_mode: host` 改为端口映射、增加命名卷 |
| 功能 | `/content` 改为引用计数 + 两段式回收：请求结束后按 `-disconnect-ttl` 断开该种子的 peer 连接、按 `-idle-ttl` 移除种子记录；新增 `-idle-ttl`、`-disconnect-ttl`、`-info-timeout`、`-keep-meta` 四个参数 |
| 功能 | 启动时不再把历史种子全量加载进内存，改由 `/content` 与 `/list` 按需创建；`/list` 纳入同一套注册表，元数据超时返回 504 |
| 功能 | `/content` 的读取跟随请求上下文，客户端断开后不再阻塞在等分片上，种子可正常回收 |
| 文档 | 新增 `docs/`：架构、种子生命周期、内容读取链路三篇实现文档，并把 `API.md`、`OPS.md` 迁入其中 |

## HTTP API

##### URL 参数

* **playlist** - 输出文件格式，取值之一：`m3u`、`html`、`json`
* **hash** - 种子 info hash。示例：`08ada5a7a6183aae1e09d831df6748d566095a10`
* **extsWhitelist** - 白名单扩展名列表。可选值为 `-`（不限）或以逗号分隔的扩展名列表。示例：`-`、`mp3,mp4a`
* **tagsBlacklist** - 黑名单标签列表，标签从文件名中提取。可选值为 `-`（不过滤）或以逗号分隔的标签列表。完整可选标签见 `/playlist/tags.go`。示例：`-`、`remix,interview`

通过 magnet 链接获取文件列表：

```
GET http://localhost/list/{playlist}/{extsWhitelist}/{tagsBlacklist}/magnet/{magnetURI}
```

通过 info hash 获取文件列表：

```
GET http://localhost/list/{playlist}/{extsWhitelist}/{tagsBlacklist}/hash/{hash}
```

下载文件：

```
GET http://localhost/content/{hash}/{filePath}
```

## 使用示例

通过种子 hash 获取 Sintel 的 HTML 链接列表：

```
http://localhost/list/html/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
```

或通过 magnet 链接：

```
http://localhost/list/html/mp4/-/magnet/magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fsintel.torrent
```

然后用 VLC 播放 Sintel.mp4：

```
vlc http://localhost/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4
```

或用 VLC 直接打开 m3u 播放列表：

```
vlc http://localhost/list/m3u/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
```

用 wget 加 bash 脚本从一组 magnet 链接批量生成 **m3u** 文件：
<details>
  <summary>makelist.sh</summary>

```bash
#!/bin/bash

magnets="magnets.txt"
playlist_dir="playlists" 

host="localhost"
port="8484"

exts="mp3,mp4,m4a,m4,flac"
exclude_tags="live,interlude,demo,orchestral,orch,remix,mix,acoustic,instr,instrumental,synthesis,bonus,heavy"

rm -f $playlist_dir/*m3u*

while read line; do
     wget --content-disposition --directory-prefix=$playlist_dir --tries=3 "http://$host:$port/list/m3u/$exts/$exclude_tags/magnet/$line"
done < $magnets
```

</details>
<br>

## Docker

启动容器并暴露 HTTP 端口 8484：

```
docker run --volume downloads:/downloads -it -p 8484:80 winpooh32/peerstohttp:latest -dir=/downloads
```

## 编译步骤

uTP 依赖需要 C 编译器，请先安装：

* Windows：下载并安装 <https://jmeubank.github.io/tdm-gcc/download/>
* Ubuntu Linux：`apt install build-essential`

获取代码：

```
go get -v -u github.com/WinPooh32/peerstohttp
```

以 vendor 模式编译：

```
go build -mod=vendor -o peerstohttp
```

安装：

```
go install -i github.com/WinPooh32/peerstohttp/cmd
```

运行：

```
go run github.com/WinPooh32/peerstohttp/cmd -port=8484 -dir="/path/to/download"
```

* `$GOPATH` 通常为 `~/go`；
* `dir` 的默认值是系统临时目录，查看全部可用选项请执行 `./peerstohttp -help`。

## License

本仓库以 **GNU General Public License v3.0** 发布，完整条款见 [LICENSE](LICENSE)。

- **原始项目**：[WinPooh32/peerstohttp](https://github.com/WinPooh32/peerstohttp)，版权归原作者所有
- **本仓库**：上游的非官方衍生版本；对上游的修改（vendor 编译补丁、`DEPLOY.md`、`start.ps1`、`docker-compose.yml`、种子生命周期管理）同样以 GPL-3.0 发布
- **同步**：本仓库不保证与上游保持同步

下方徽章反映的是**上游项目**的 FOSSA 扫描状态，不代表本仓库：

[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp.svg?type=large)](https://app.fossa.io/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp?ref=badge_large)
