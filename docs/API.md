# peerstohttp HTTP API

`peerstohttp` 把 BitTorrent 转成 HTTP 流：给它一个 magnet 或 info hash，它返回文件清单（`json` / `m3u` / `html`）；请求清单里的某个文件，它一边从 BT 网络下载，一边通过 HTTP 把字节流吐给播放器。

- 服务框架：Go + [go-chi](https://github.com/go-chi/chi)
- 默认监听：`0.0.0.0:80`（可用 `-host` / `-port` 覆盖，文档示例多使用 `8484`）
- 接口风格：3 个只读 `GET` 端点，无鉴权，CORS 全开（`AllowedOrigins: *`）
- 本文档与源码的对应关系：路由见 `http/api.go`，参数校验见 `http/handle.go`，响应渲染见 `http/render/`，清单结构见 `playlist/list.go`

---

## 1. 端点总表

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | `/list/{json\|m3u\|html}/{whitelist}/{ignoretags}/hash/{infoHash}` | 按 info hash 列出文件清单 |
| GET | `/list/{json\|m3u\|html}/{whitelist}/{ignoretags}/magnet/{magnetURI}` | 按 magnet 列出文件清单 |
| GET | `/content/{infoHash}/{种子名}/{种子内路径}` | 流式读取单个文件，支持 Range |
| GET | `/debug/pprof/*` | 仅启动时带 `-profile` 才注册 |

> `/list/` 后的第一个路径段由 chi 正则约束为 `[json,m3u,html]`，其他取值不会命中路由，直接返回 404。

---

## 2. 路径参数

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `ContentType` | enum | 是 | `json` / `m3u` / `html` |
| `whitelist` | string | 是 | 扩展名白名单。`-` 表示不过滤；否则逗号分隔、**不带点**，如 `mp4,mp3`（内部自动补 `.`） |
| `ignoretags` | string | 是 | 标签黑名单。`-` 表示不过滤；否则逗号分隔，如 `live,remix,demo` |
| `infoHash` | string | 是 | 40 位十六进制 SHA1，非法则 400 |
| `magnetURI` | string | 是 | 完整 magnet。路径段与 `?` 之后的 query 会被拼接后再解析，因此 `&tr=...` 等参数可直接写在 URL 里 |
| `种子名` | string | 是 | torrent 的 `name`，即清单里 `header.name` 的值 |
| `种子内路径` | string | 否 | 文件在种子内的相对路径，需 URL 转义；单文件种子可省略 |

### 标签黑名单可选值

来源 `playlist/tags.go`，共 33 个，匹配大小写不敏感：

```
instrumental, instr, acoustic, interview, orchestral, orch, mix, remix, remixes,
remastered, remaster, japanese, japan, cd, interlude, vinyl, lp, limited, ep,
extended, clean, single, demo, capella, acapella, synthesis, live, album, bonus,
radio, original, full, heavy
```

标签从文件名中提取，`_` `-` `.` 会被视为分隔符；`(…)` / `[…]` 内的标签属于 "inside" 作用域，只有部分标签支持该作用域。

---

## 3. 端点详情

### 3.1 按 info hash 列出清单

```
GET /list/{ContentType}/{whitelist}/{ignoretags}/hash/{infoHash}
```

示例：

```
http://localhost:8484/list/html/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
http://localhost:8484/list/m3u/mp3,flac/live,remix/hash/08ada5a7a6183aae1e09d831df6748d566095a10
http://localhost:8484/list/json/-/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10
```

行为：以 info hash 向 BT 网络请求元数据 → 阻塞等待 `GotInfo()` → 生成清单 → 把 metainfo 落库（`<下载目录>/.app.bolt.db`）。

### 3.2 按 magnet 列出清单

```
GET /list/{ContentType}/{whitelist}/{ignoretags}/magnet/{magnetURI}
```

示例（magnet 中含 `&` 时，整串直接放在路径后即可）：

```
http://localhost:8484/list/m3u/mp4/-/magnet/magnet:?xt=urn:btih:08ada5a7a6183aae1e09d831df6748d566095a10&dn=Sintel&tr=udp%3A%2F%2Fexplodie.org%3A6969
```

行为与 3.1 一致，区别只是元数据获取方式为 `AddMagnet`；解析失败返回 400 并回显解析错误信息。

### 3.3 流式读取文件

```
GET /content/{infoHash}/{种子名}/{种子内路径}
GET /content/{infoHash}/{种子名}          # 单文件种子（文件数 <= 1）
```

示例：

```
http://localhost:8484/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4
```

关键点：

- **路径必须包含种子名**。源码中文件路径由 `种子名 + "/" + 种子内相对路径` 拼接（`vendor/github.com/anacrolix/torrent/t.go:216`），`findFile` 按该字符串精确匹配（`http/helpers.go:138`）。仅写种子内相对路径会匹配失败。
- **支持 HTTP Range**：底层是 `torrent.Reader`（实现了 `io.ReadSeeker`）+ `http.ServeContent`，播放器可自由拖动进度条，返回 `206 Partial Content`。
- **未下载完也能读**：`reader.SetResponsive()` 允许读取尚未校验完成的分片。
- **预读 = 文件大小的 10%**：大文件会占用较多内存。
- 响应头会带 `Content-Disposition: filename="<文件名>"`。
- 若请求的 hash 尚未被服务端接管，会即时 `AddTorrentInfoHash` 并等待元数据。
- **目录路径的坑**：目录型种子且路径不含 `/` 时走 `serveTorrentDir`，该函数当前是空实现（打包 zip 的代码已注释），因此返回 **200 + 空响应体**。

---

## 4. 响应格式

### 4.1 JSON

`Content-Type: application/json; charset=utf-8`

```json
{
  "header": {
    "hash": "08ada5a7a6183aae1e09d831df6748d566095a10",
    "name": "Sintel",
    "files": 12
  },
  "content": [
    {
      "name": "Sintel",
      "name_orig": "Sintel.mp4",
      "ext": ".mp4",
      "mime": "video/mp4",
      "size": 129241262,
      "path": ["Sintel.mp4"],
      "tags": []
    }
  ]
}
```

| 字段 | 说明 |
|---|---|
| `header.hash` | info hash |
| `header.name` | 种子名（也是 `/content` 路径里必须带的那一段） |
| `header.files` | 种子内文件总数（**未经过滤的原始数量**） |
| `content[].name` | 展示名。音频文件会走 `playlist/name` 清洗（去标签、去序号） |
| `content[].name_orig` | 原始文件名 |
| `content[].ext` | 含点的扩展名 |
| `content[].mime` | `mime.TypeByExtension` 推断，未知为空 |
| `content[].size` | 字节数 |
| `content[].path` | 种子内路径分段数组，**不含种子名** |
| `content[].tags` | 从路径/文件名提取到的标签 |

> `content[].path` 不含种子名，不能直接拼出可播放 URL。需要可播链接时请用 `m3u` 输出，或自行补上 `header.name`。

过滤逻辑：`ignoretags` 命中任一标签即剔除；`whitelist` 非空时仅保留扩展名命中项（`playlist/list.go:51-70`）。

### 4.2 M3U

`Content-Type: application/x-mpegURL; charset=utf-8`
`Content-Disposition: filename="<种子名>.m3u8"`

```m3u
#EXTM3U
#EXTENC: UTF-8
#EXTINF:-1,Sintel
http://localhost:8484/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4
```

- 条目 URL 使用请求的 `scheme://Host` 拼绝对地址，因此**必须通过 `/list/m3u/...` 生成**，不能手抄。
- 多级目录文件显示为 `父目录/文件名`。

### 4.3 HTML

`Content-Type: text/html; charset=utf-8`
`Content-Disposition: filename="<种子名>.html"`

```html
<a href="/content/{hash}/{name}/{path}">专辑/曲目.mp3</a></br>
```

只是一层链接列表，便于人工点选。

---

## 5. 状态码约定

| 状态码 | 触发条件 |
|---|---|
| `200` | 正常返回清单 / 文件流 |
| `206` | `/content` 收到合法 `Range` 请求 |
| `400` | `infoHash` 不是合法 SHA1；magnet 解析失败（回显错误文本）；`/content` 路径未通过 URL 校验 |
| `404` | 未能从 BT 网络追踪到种子（拿不到元数据） |
| `408` | 处理过程中请求上下文被取消 |
| `500` | `/content` 找不到匹配文件（`errFileNotFound`），或写流失败 |

> 注意：文件不存在返回的是 **500**，不是 404。排查 `/content` 报 500 时，优先检查路径是否缺少种子名。

---

## 6. 行为特性与使用注意事项

1. **首次请求会阻塞**：`/list` 和 `/content` 都要等待 `GotInfo()`。无活跃 peer、死种或防火墙拦截时，请求会一直挂起直到客户端超时；建议客户端设置超时，例如 `curl --max-time 60`。
2. **清单可访问不代表文件可下载**：清单只解析元数据，实际吞吐取决于 peer 数量与带宽。
3. **种子会持久化**：每次 `/list` 追踪成功后把 metainfo 写入 `<下载目录>/.app.bolt.db`，进程重启后自动恢复，无需重复提交 magnet。
4. **过滤只在清单层生效**：`whitelist` / `ignoretags` 只影响 `/list` 返回的条目，不限制 `/content` 直接访问其他文件。
5. **边下边播无需额外组件**：不需要 ffmpeg / HLS / 转码服务。浏览器能否直接播放取决于容器编码（MP4/H.264 可，MKV 一般不行），这与"边下边播"是两件不同的事。
6. **CORS**：允许全部来源，方法与头为 `GET, POST, PUT, DELETE, PATCH, OPTIONS` 与 `Accept, Authorization, Content-Type, X-CSRF-Token`。
7. **调试端点**：启动加 `-profile` 后开放 `/debug/pprof/`、`cmdline`、`profile`、`symbol`、`trace`、`goroutine`、`heap`、`threadcreate`、`block`、`allocs`、`mutex`。

---

## 7. 快速验证

```bash
# 1) 服务存活 + 错误处理（非法 hash 应返回 400）
curl -s -o /dev/null -w "HTTP %{http_code}\n" --max-time 10 \
  "http://127.0.0.1:8484/list/json/-/-/hash/zzz"

# 2) 清单接口
curl -s --max-time 60 \
  "http://127.0.0.1:8484/list/json/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10"

# 3) Range 请求（应返回 206，且 Content-Range 正确）
curl -s -o /dev/null -D - -H "Range: bytes=1000-2023" --max-time 90 \
  "http://127.0.0.1:8484/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4"

# 4) 用播放器直接播清单
vlc "http://127.0.0.1:8484/list/m3u/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10"
```

Windows PowerShell 下 `curl` 是 `Invoke-WebRequest` 的别名，需写成 `curl.exe`。

---

## 8. 与上游文档的差异

`README.md` 中把下载接口写作 `GET /content/{hash}/{filePath}`，实际实现要求路径包含种子名：

- ✅ `/content/{infoHash}/{种子名}/{种子内路径}`
- ✅ `/content/{infoHash}/{种子名}`（单文件种子）
- ❌ `/content/{infoHash}/{种子内路径}`

所以以 `/list/json` 的 `content[].path` 拼接 URL 会失败，应改用 `/list/m3u/...` 生成的链接。

---

部署、编译与故障排查见 [DEPLOY.md](../DEPLOY.md)，运维细节见 [OPS.md](OPS.md)，实现细节见 [architecture.md](architecture.md)。
