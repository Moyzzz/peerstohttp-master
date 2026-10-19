# peerstohttp

[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp?ref=badge_shield)
[![Go Report Card](https://goreportcard.com/badge/github.com/WinPooh32/peerstohttp)](https://goreportcard.com/report/github.com/WinPooh32/peerstohttp)

Simple torrent proxy to http stream controlled over REST-like api

Built top on [anacrolix/torrent](https://github.com/anacrolix/torrent) lib.

> **本地部署与启动说明见 [DEPLOY.md](DEPLOY.md)**：覆盖 Windows / Linux / Docker Compose 三种方式，含编译依赖、启动参数、验证清单与常见问题排查。

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

## HTTP API

##### URL Parameters

* **playlist** - output file format, one of these values: `m3u`,`html`,`json`
* **hash** - torrent info hash. Example: `08ada5a7a6183aae1e09d831df6748d566095a10`
* **extsWhitelist** - list of whitelisted file extensions. Possible values: "-" (any) or list extension names divided by comma. Examples: "`-`", "`mp3,mp4a`"
* **tagsBlacklist** - list of blacklisted tags, extracted from file names. Possible values: "-" (no filter) or list tags divided by comma. See /playlist/tags.go for full list of possible tags. Examples: "`-`", "`remix,interview`"

Get list of files by magnet uri:

```
GET http://localhost/list/{playlist}/{extsWhitelist}/{tagsBlacklist}/magnet/{magnetURI}
```

Get list of files by info hash:

```
GET http://localhost/list/{playlist}/{extsWhitelist}/{tagsBlacklist}/hash/{hash}
```

Download file:

```
GET http://localhost/content/{hash}/{filePath}
```

## Examples

Get HTML links list for Sintel by torrent hash:

```
http://localhost/list/html/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
```

or by magnet URI:

```
http://localhost/list/html/mp4/-/magnet/magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel&tr=udp%3A%2F%2Fexplodie.org%3A6969&tr=udp%3A%2F%2Ftracker.coppersurfer.tk%3A6969&tr=udp%3A%2F%2Ftracker.empire-js.us%3A1337&tr=udp%3A%2F%2Ftracker.leechers-paradise.org%3A6969&tr=udp%3A%2F%2Ftracker.opentrackr.org%3A1337&tr=wss%3A%2F%2Ftracker.btorrent.xyz&tr=wss%3A%2F%2Ftracker.fastcast.nz&tr=wss%3A%2F%2Ftracker.openwebtorrent.com&ws=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2F&xs=https%3A%2F%2Fwebtorrent.io%2Ftorrents%2Fsintel.torrent
```

Then watch Sintel.mp4 using VLC video player:

```
vlc http://localhost/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4
```

Or open m3u playlist in VLC video player:

```
vlc http://localhost/list/m3u/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
```

Generate **m3u** files from list of magnets using wget and bash script:
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

Run container with exposed 8484 http port:

```
docker run --volume downloads:/downloads -it -p 8484:80 winpooh32/peerstohttp:latest -dir=/downloads
```

## Build steps

utp dependency requires C compiler, then install it:  

* Windows: download and install <https://jmeubank.github.io/tdm-gcc/download/>
* Ubuntu linux: `apt install build-essential`

Download:

```
go get -v -u github.com/WinPooh32/peerstohttp
```

Build in vendor mode:

```
go build -mod=vendor -o peerstohttp
```

Install:

```
go install -i github.com/WinPooh32/peerstohttp/cmd
```

Run:

```
go run github.com/WinPooh32/peerstohttp/cmd -port=8484 -dir="/path/to/download"
```

* Usually, $GOPATH is "~/go";
* Default `dir` value is system tmp folder, for listing all possible options run `$ ./peerstohttp -help`.

## License

本仓库以 **GNU General Public License v3.0** 发布，完整条款见 [LICENSE](LICENSE)。

- **原始项目**：[WinPooh32/peerstohttp](https://github.com/WinPooh32/peerstohttp)，版权归原作者所有
- **本仓库**：上游的非官方衍生版本；对上游的修改（vendor 编译补丁、`DEPLOY.md`、`start.ps1`、`docker-compose.yml`）同样以 GPL-3.0 发布
- **同步**：本仓库不保证与上游保持同步

下方徽章反映的是**上游项目**的 FOSSA 扫描状态，不代表本仓库：

[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp.svg?type=large)](https://app.fossa.io/projects/git%2Bgithub.com%2FWinPooh32%2Fpeerstohttp?ref=badge_large)
