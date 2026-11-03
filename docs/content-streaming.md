# 内容读取链路

本文说明 `/content` 从路由到分片读取的完整过程，重点解释 `ctxReadSeeker` 为什么存在，以及排障时该看哪些日志。

## 1. 路由与参数

```go
r.With(hash, path).Get("/content/{"+paramHash+"}/*", h.content)
```

两个中间件（`http/handle.go`）：

| 中间件 | 作用 | 失败响应 |
|---|---|---|
| `hash` | 取 `{hash}` 并 `govalidator.IsSHA1` 校验，转小写存入 ctx | 400 |
| `path` | 对通配段 `*` 做 `QueryUnescape`，再用 `govalidator.IsRequestURI` 校验 | 400 |

因此 handler 里拿到的是已解码的种子内路径，例如
`[Studio GreenTea] Ao no Hako S2 [03][WebRip][HEVC-10bit 1080p AAC][JPSC].mp4`。

## 2. 定位文件

```go
func findFile(t *torrent.Torrent, path string) (*torrent.File, bool)
```

- **单文件种子**（`t.Info().IsDir() == false`）：只校验 `filepath.Base(path) == t.Info().Name`，直接取 `t.Files()[0]`；
- **目录种子**：遍历 `t.Files()`，按 `f.Path()` 精确匹配。

匹配失败返回 `errFileNotFound`，handler 只记录日志、不改状态码 —— 此时 `http.ServeContent` 可能已经写出响应头。

> 目录种子的根路径请求（路径里没有 `/`）会走 `serveTorrentDir`，该函数目前是**空实现**（zip 打包逻辑被注释掉了），直接返回 nil。也就是说请求目录根只会得到一个空响应。

## 3. 读取

```go
file, _ := findFile(t, path)
reader := file.NewReader()      // 每次请求新建一个"读窗口"
defer reader.Close()
return serveContent(w, r, file.Length(), reader, name)
```

`file.NewReader()` 最终走到库里的 `t.addReader(&r)`：它只是把 reader 注册进 `t.readers`，并触发 `readerPiecePriorities()` 重算 —— 即"把这段字节范围标成 Now / Readahead 优先级"。

**reader 不创建 peer 连接**：连接挂在 `*Torrent` 上（`t.conns`），reader 只影响"优先下载哪些分片"。所以同一播放会话内的多个 Range 请求复用同一批连接，不会重连。

## 4. serveContent

```go
reader.SetResponsive()                    // 分片未校验完成也先吐数据，降低首字节延迟
if size > 0 {
	reader.SetReadahead((size * 10) / 100) // 预读文件大小的 10%
}
w.Header().Set("Content-Disposition", `filename="`+url.PathEscape(name)+`"`)
reader.Seek(0, 0)
http.ServeContent(w, r, name, time.Now(), ctxReadSeeker{Reader: reader, ctx: r.Context()})
```

职责划分：

| 关注点 | 由谁负责 |
|---|---|
| `Range` 解析、`Content-Range`、206、`If-Range`、多段 Range | 标准库 `http.ServeContent` |
| 分片优先级、预读窗口、peer 调度 | `anacrolix/torrent` 的 reader |
| 读取随请求取消 | 本仓库的 `ctxReadSeeker` 包装 |

不要自己重写 Range 逻辑：`ServeContent` 已经处理了 HEAD、条件请求、多段 Range 合并等边界，重写只会引入回归。

## 5. 为什么需要 ctxReadSeeker

库里的 reader 实现是：

```go
// vendor/github.com/anacrolix/torrent/reader.go
func (r *reader) Read(b []byte) (n int, err error) {
	return r.ReadContext(context.Background(), b)
}
```

而 `http.ServeContent` 只会调用 `io.Reader.Read`。于是当客户端断开、而 handler 正阻塞在 `waitAvailable`（等分片到达）时：

- 读取用的是 `context.Background()`，永远等不到取消信号；
- handler 不返回 → `defer release()` 不执行 → `refs` 永远 ≥ 1；
- 两个定时器都不会启动，种子与连接永久占用。

修复方式是包一层，把读取接到请求上下文：

```go
type ctxReadSeeker struct {
	torrent.Reader
	ctx context.Context
}

func (c ctxReadSeeker) Read(b []byte) (int, error) {
	return c.Reader.ReadContext(c.ctx, b)
}
```

嵌入接口而不是手写全部方法，`Seek`/`Close`/`SetReadahead` 等自动转发，只覆写 `Read` 一个方法，`io.ReadSeeker` 约束依然满足。

验证方法（离线可复现）：

1. 造一个"有 metainfo、没有任何 completed 分片"的种子，读取必然阻塞；
2. 用 `curl --max-time 2` 请求，2 秒后强制断开；
3. 修复前：十几秒内不会出现 `torrent idle`；修复后：断开后 1~2 秒内出现 `torrent idle`。

## 6. 预读窗口的副作用

`SetReadahead(size * 10 / 100)` 是按**文件大小**比例算的：

| 文件 | 预读窗口 |
|---|---|
| 2 GB | 200 MB |
| 20 GB | 2 GB |

这会让一次请求就把大量分片标为"即将需要"，从而放大实际下载量。它与 `-max-connections` 需要匹配：连接少而预读大，会形成"很多分片在排隊但没有 peer 供给"的局面；连接多而预读小，多出来的 peer 会闲置。

调整时注意 `SetReadahead` 是**每请求**调用的，多个并发 Range 的窗口会取并集。

## 7. 排障日志

### 生命周期三条（`app` 层）

```
INF torrent created  hash=...
INF torrent idle: peer connections closed ... hash=... keep_meta=...
INF torrent dropped after idle grace period hash=...
```

### 读取相关（`http` 层）

```
INF file path = <种子内路径>
```

表示已定位到文件、准备开始写响应。每个 `/content` 请求一条。

### 访问日志（chi `middleware.Logger`）

```
2026/11/02 19:15:07 [HOST/xxxx-000032] "GET http://127.0.0.1:8484/content/... HTTP/1.1" from 127.0.0.1:1182 - 200 261B in 0s
```

| 片段 | 含义 |
|---|---|
| `[HOST/xxxx-000032]` | 请求 ID（主机名 + 随机串 + 递增序号），可用来对齐同一请求的多行日志 |
| `200` / `206` | 完整响应 / Range 分段响应 |
| `261B` | 响应体字节数 |
| `in 0s` | 处理耗时（`0s` 表示不足 1ms） |

常见组合：

- 只有 `file path` 没有访问日志 → 请求还在进行中，或 handler 卡住（`ctxReadSeeker` 修复了后者的常见成因）；
- `200 0B in Xs` → 响应头已发出但正文为空，通常是客户端在读到数据前断开；
- `504` → 元数据超时（`-info-timeout`），与读取无关。

> zerolog 写 stdout，chi 的访问日志走标准库 `log`（stderr）。两个流的相对顺序不保证，不要用它们判断因果；判断先后请看 zerolog 内部顺序或请求 ID 序号。

## 8. 相关文档

- 种子从创建到回收：[torrent-lifecycle.md](torrent-lifecycle.md)
- 分层与数据流全景：[architecture.md](architecture.md)
- 接口参数与状态码：[API.md](API.md)
