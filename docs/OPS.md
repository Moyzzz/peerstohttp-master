# peerstohttp 运维手册（启动命令与参数全集）

> 本文件只解决一件事：**怎么把服务启动起来，以及每个启动参数到底做什么**。
> 编译环境准备、故障原理、依赖补丁见 [DEPLOY.md](../DEPLOY.md)；接口用法见 [API.md](API.md)；实现细节见 [architecture.md](architecture.md)。
>
> 事实基线：`settings/settings.go`（参数定义）、`app/init.go`（参数生效位置）、`cmd/`（进程入口）。
> 本快照内 **没有预编译的 `peerstohttp.exe`**，首次使用必须先编译（见 §3.3）。

---

## 0 一页速查

```powershell
# Windows 前台（最常见）
.\peerstohttp.exe -port=8484 -port-torr=6881 -dir=C:/bt-data -cache-capacity=0
```

```bash
# Linux 二进制
./peerstohttp -port=8484 -port-torr=6881 -dir=/data/bt -cache-capacity=0
```

```powershell
# Windows 一键脚本（Toolchain/DLL/端口检查都帮你做了）
.\start.ps1 -Port 8484 -TorrentPort 6881 -Dir C:/bt-data
```

```bash
# 容器
docker run -d --name peerstohttp -p 8484:80 -p 6881:6881/tcp -p 6881:6881/udp \
  -v peerstohttp-data:/downloads winpooh32/peerstohttp:latest -dir=/downloads -port-torr=6881
```

```powershell
# 查看全部参数（唯一的"文档"是它自己的 usage）
.\peerstohttp.exe -h
```

**三条铁律**

1. `-dir` 必须显式给。留空会落到系统临时目录，**进程退出时整个目录被 `os.RemoveAll` 删除**（`app/app.go:54`、`app/app.go:197`）。
2. `-cache-capacity` 是"磁盘/缓存总量上限"，**不是加速开关**。默认 10240 MiB，超限按 LRU 淘汰旧文件；要永久保留文件就设 `0`。
3. 同一台机器**只能跑一个实例**：BT 端口固定时第二个实例会因 `bind: Only one usage of each socket address` 直接 FTL 退出。

---

## 1 启动方式总览

本程序**没有子命令**（无 `peerstohttp serve` 之类），进程行为 100% 由启动参数决定。五种入口，参数拼法不同，参数**名字与含义完全一致**。

| # | 方式 | 前置条件 | 参数放在哪 | 适用 |
|---|---|---|---|---|
| 1 | 原生二进制 | 已编译 + 运行库 DLL | 直接跟在 exe 后 | Win/Linux 主力 |
| 2 | `start.ps1` | Windows + 已编译 | 用脚本自己的 `-Port/-Dir/...`，**不透传**原始 flag | Windows 快速起停 |
| 3 | `go run` / `go build` | Go + gcc（CGO） | 跟在包路径之后 | 开发调试 |
| 4 | `docker run` | Docker | **镜像名之后** | 免环境 |
| 5 | `docker compose` | Docker Compose | 写进 `command:` 列表 | 长期运行 |

参数位置示意（顺序错了会被当成"未知参数"，或直接不生效）：

```text
     [程序]     [参数...]                     ✅ 正确
./peerstohttp  -port=8484 -dir=/data/bt

     [docker run] [选项...] [镜像] [参数...]   ✅ 正确
docker run -d -p 8484:80  image  -port=80 -dir=/downloads

     [docker run] [选项...] [参数前移] [镜像]  ❌ 错误
docker run -d -port=80  image
```

---

## 2 参数全集（23 个）

### 2.1 flag 语法

Go 标准 `flag` 包，规则固定：

| 写法 | 结果 |
|---|---|
| `-port=8484` | ✅ 推荐，最稳 |
| `-port 8484` | ✅ 等价 |
| `--port=8484` | ✅ 单/双横线都认 |
| `-no-dht` | ✅ 布尔参数，等价 `-no-dht=true` |
| `-no-dht false` | ❌ **不生效**：`false` 被当成位置参数，解析立刻终止，后面的参数全部丢失 |
| `-port` （缺值） | ❌ 报 `flag needs an argument` 退出码 2 |
| `-h` / `-help` | ✅ 打印 usage 后退出 |

> 关键陷阱：Go 的 flag 解析遇到**第一个不以 `-` 开头的参数就停下**。所以参数必须写在所有位置参数之前；反过来说，容器里 `ENTRYPOINT ./peerstohttp` + `docker run image -port=80` 之所以成立，是因为镜像名被 Docker 吃掉、不进入 argv。

### 2.2 全部参数表

#### 网络与端口

| 参数 | 类型 | 默认值 | 单位/取值 | 说明 | 风险 |
|---|---|---|---|---|---|
| `-host` | string | `0.0.0.0` | IP | HTTP 监听地址（`host:port`）。只给本机用改 `127.0.0.1` | 默认全网卡暴露 |
| `-port` | int | `80` | 1-65535 | HTTP 端口。Windows 上 80 常被 `http.sys` 占用 | 容器内监听非 80 需同步改 `-p` 右侧 |
| `-port-torr` | int | `0` | 1-65535，`0`=随机 | BT 监听端口（TCP+UDP 同号） | 随机端口无法做映射，连通率差 |
| `-http-proxy` | string | 空 | URL | tracker 请求走的 HTTP 代理，如 `http://127.0.0.1:7890`。**只代理 tracker/种子源，不走 P2P 流量** | 填错会导致永远拿不到元数据 |

#### 存储

| 参数 | 类型 | 默认值 | 单位/取值 | 说明 | 风险 |
|---|---|---|---|---|---|
| `-dir` | string | 空 | 路径（可相对） | 下载目录，启动时 `MkdirAll` 自动创建；元数据库落在 `{dir}/.app.bolt.db` | **留空 = 用临时目录 + 退出即删数据** |
| `-cache-capacity` | int64 | `10240` | **MiB**，`≤0` 关闭上限 | 文件缓存总量上限。`>0` 走块级 LRU（超限淘汰已下载文件）；`≤0` 走普通落盘 | 设太大占磁盘，设太小播到一半文件被淘汰 |

#### 限速与连接

| 参数 | 类型 | 默认值 | 单位/取值 | 说明 |
|---|---|---|---|---|
| `-down-rate` | int | `0` | **KiB/s**，`0`=不限 | 客户端级全局限速（不是单种子） |
| `-up-rate` | int | `0` | **KiB/s**，`0`=不限 | 程序硬编码 `cfg.Seed = true`，**默认在做种**，公网必须限上行 |
| `-max-connections` | int | `50` | 个 | 每个种子的 peer 连接上限，同时作用于 `EstablishedConnsPerTorrent` 与 `TorrentPeersLowWater` |

#### 种子生命周期（内存与连接回收）

| 参数 | 类型 | 默认值 | 单位/取值 | 说明 | 风险 |
|---|---|---|---|---|---|
| `-idle-ttl` | int | `90` | 秒 | 最后一个请求结束后，再等这么多秒仍无新请求，才 `Drop()` 移除种子记录 | 太小：播放器拖动/切集时反复重建种子（`onSetInfo` 为 O(分片数)），表现为 CPU 抖动；太大：内存回落慢 |
| `-disconnect-ttl` | int | `15` | 秒，`0` = 立即 | 最后一个请求结束后，等这么多秒仍无新请求，才断开该种子全部 peer 连接（`SetMaxEstablishedConns(0)`）。用于吸收播放器"缓冲满→中止请求→再发新 Range"的间隙 | 太小：每个间隙都要重新拨号与重排分片优先级，续播/拖动变慢；太大：连接释放得晚，空闲种子的内存回落慢 |
| `-info-timeout` | int | `30` | 秒 | 等待种子元数据的上限，`/content` 与 `/list` 共用。超时返回 **504**，且本次新建的种子立即回收 | 太小：冷门种子首次打开容易 504；太大：无可用 peer 时请求长时间挂住 |
| `-keep-meta` | bool | `true` | — | 种子被 `Drop()` 后是否保留 `.app.bolt.db` 里的 metainfo。保留则下次访问直接注入、秒出 info；`false` 则连缓存一起删，下次需重新交换元数据 | 置 `false` 后磁盘不再累积历史记录，但重复访问同一视频会变慢 |

> 四个参数共同构成"引用计数 + 两段式回收"：请求结束 → 宽限 `-disconnect-ttl` 后断连（释放连接缓冲）→ 空闲 `-idle-ttl` 后移除记录（释放按分片数计算的元数据）。
> 两个定时器分别由 `-disconnect-ttl` 与 `-idle-ttl` 控制，且前者会被自动收敛到不超过后者（`disconnectTTL = min(disconnectTTL, idleTTL)`），避免阶段一永远不发生。
> 启动时**不再**把 `.app.bolt.db` 里的历史种子加载进内存，只在首个请求命中时按需创建；因此常驻内存只与"当前活跃种子数"相关，与历史访问量无关。
> 需要一次性清空全部记录时，停服后删 `{dir}/.app.bolt.db`（见 §4）。

#### 协议开关（全部布尔，默认 `false` = 开启该特性）

| 参数 | 关掉之后 | 建议 |
|---|---|---|
| `-no-dht` | 关 DHT，只剩 tracker 发现 peer | **一般不要关**，关了冷门种子基本无解 |
| `-no-upnp` | 关 UPnP 自动端口映射 | 容器内、已手工端口映射时必加 |
| `-no-tcp` | 关 TCP，只留 uTP | 视网络环境 |
| `-no-utp` | 关 uTP，只留 TCP | 注意：**不影响编译**，CGO 依赖仍在 |
| `-no-ipv4` | 关 IPv4 | 少用 |
| `-no-ipv6` | 关 IPv6 | 机器无 v6 或防火墙不放行时开 |
| `-force-encryption` | —（默认关） | 打开后强制 MSE/RC4，`CryptoProvides` 只留 RC4 | 特定网络（如校园网/ISP 干扰）才需要，会降低兼容性 |

#### 日志与调试

| 参数 | 类型 | 默认值 | 说明 | 风险 |
|---|---|---|---|---|
| `-json-logs` | bool | `false` | 日志改 JSON 单行输出，便于采集 | 无 |
| `-torr-debug` | bool | `false` | 打开 anacrolix/torrent 底层日志（默认被 Discard），排障专用 | 日志量巨大 |
| `-profile` | bool | `false` | 挂载 `/debug/pprof/*`（含 `heap` 可 dump 内存） | **公网绝对不要开** |

### 2.3 单位换算与边界

| 参数 | 你填 | 实际生效 |
|---|---|---|
| `-cache-capacity=10240` | 10240 MiB | 10737418240 字节（源码 `<<20`） |
| `-cache-capacity=2048` | 2 GiB | 约 2000 个 1 GiB 文件后开始淘汰 |
| `-cache-capacity=0` | — | 走 `storage.NewFile`，文件永久保留 |
| `-down-rate=8192` | 8192 KiB/s | ≈ 8 MiB/s ≈ 64 Mbps |
| `-up-rate=512` | 512 KiB/s | ≈ 4 Mbps |
| `-down-rate=8` | 8 KiB/s | 实际 burst 被抬到 16 MiB，等于"几乎不限速"——**限速值不要填得太小** |

---

## 3 各方式启动命令（可直接复制）

### 3.1 Windows 原生二进制

前置：`peerstohttp.exe` 与 3 个运行库同目录，否则**双击无反应、无任何日志**：

```text
libstdc++-6.dll
libgcc_s_seh-1.dll
libwinpthread-1.dll
```

```powershell
cd C:/path/to/peerstohttp-master

# 前台（Ctrl+C 停止）
.\peerstohttp.exe -port=8484 -port-torr=6881 -dir=C:/bt-data -cache-capacity=0

# 后台（隐藏窗口）
Start-Process -FilePath ".\peerstohttp.exe" `
  -ArgumentList "-port=8484","-port-torr=6881","-dir=C:/bt-data" `
  -WorkingDirectory (Get-Location) -WindowStyle Hidden
```

### 3.2 Windows 一键脚本 `start.ps1`

脚本自己只有这几个参数，**不能透传** `-cache-capacity`、`-no-dht` 等原始 flag：

| 脚本参数 | 默认 | 对应原程序 |
|---|---|---|
| `-Port <int>` | `8484` | `-port` |
| `-TorrentPort <int>` | `6881` | `-port-torr` |
| `-Dir <path>` | `$env:PEERSTOHTTP_DIR` → `C:/bt-data` | `-dir` |
| `-Exe <path>` | `$env:PEERSTOHTTP_EXE` → `peerstohttp.exe` | 可执行文件名 |
| `-MingwBin <path>` | `$env:PEERSTOHTTP_MINGW_BIN` | 仅用于解析 DLL，不是程序参数 |
| `-Background` | 关 | 后台启动 |
| `-Stop` | 关 | 杀掉同名进程后退出 |

```powershell
.\start.ps1                                              # 前台
.\start.ps1 -Port 9000 -TorrentPort 51413 -Dir D:/bt-data # 自定义
.\start.ps1 -Background                                  # 后台
.\start.ps1 -Stop                                        # 停止
powershell -ExecutionPolicy Bypass -File .\start.ps1     # 被执行策略拦截时
```

需要额外参数（如 `-cache-capacity=0`）时，两条路：直接调 exe（§3.1），或改脚本第 193 行的 `$exeArgs` 数组。

### 3.3 从源码启动 / 编译

```powershell
# Windows：必须有 gcc，且 CGO 不能关
$env:PATH = "C:\Go\bin;C:\msys64\mingw64\bin;" + $env:PATH
$env:CGO_ENABLED = "1"
$env:CC = "gcc"

go run -mod=vendor .\cmd -port=8484 -port-torr=6881 -dir=C:/bt-data   # 直接跑
go build -mod=vendor -o peerstohttp.exe .\cmd                          # 产出二进制
```

```bash
# Linux
export CGO_ENABLED=1 CC=gcc
go build -mod=vendor -o peerstohttp ./cmd
go run -mod=vendor ./cmd -port=8484 -dir=/data/bt
```

- `go run` 时参数写在**包路径之后**；写在前面会被 go 自己吃掉。
- 交叉编译不可行（CGO），要在哪跑就在哪编。
- `-h` 也是照同样规则查看：`go run -mod=vendor ./cmd -h`。

### 3.4 Linux 二进制 / nohup

```bash
mkdir -p /data/bt
./peerstohttp -port=8484 -port-torr=6881 -dir=/data/bt -cache-capacity=0
```

```bash
# 后台
nohup ./peerstohttp -port=8484 -port-torr=6881 -dir=/data/bt \
  > peerstohttp.log 2>&1 &
echo $! > peerstohttp.pid

# 停止
kill $(cat peerstohttp.pid)
```

> 缺少 `libstdc++6` 时进程会静默退出：`sudo apt install -y libstdc++6`。

### 3.5 systemd（推荐长期运行）

仓库已提供 `deploy/systemd/peerstohttp.service`，`ExecStart` 一行即为完整启动命令：

```ini
ExecStart=/opt/peerstohttp/peerstohttp -host=127.0.0.1 -port=8484 -port-torr=6881 \
          -dir=/var/lib/peerstohttp -cache-capacity=5120 -max-connections=50 \
          -down-rate=8192 -up-rate=512 -no-upnp
```

```bash
sudo cp deploy/systemd/peerstohttp.service /etc/systemd/system/
sudo systemd-analyze verify /etc/systemd/system/peerstohttp.service
sudo systemctl daemon-reload
sudo systemctl enable --now peerstohttp
```

- `-host=127.0.0.1`：只监听回环，公网只能经 nginx 进来（配套 `deploy/nginx/peerstohttp.conf`）。
- 单元文件里 `ExecStart` **必须单行写完**，续行符 + CRLF 会让 systemd 解析失败。
- 改参数流程固定：编辑 → `daemon-reload` → `restart`。
- 刻意没加 `MemoryDenyWriteExecute` / `SystemCallFilter`，加了会因 cgo + Go 运行时映射内存直接崩。

### 3.6 docker run

容器内**默认监听 80**（`-port` 的默认值），因此映射写成 `8484:80`：

```bash
docker run -d --name peerstohttp \
  -p 8484:80 \
  -p 6881:6881/tcp \
  -p 6881:6881/udp \
  -v peerstohttp-data:/downloads \
  winpooh32/peerstohttp:latest \
  -dir=/downloads -port-torr=6881
```

```bash
docker build -t peerstohttp:local .        # 从本仓库构建（带上 vendor 补丁）
docker run -d --name peerstohttp -p 8484:80 -p 6881:6881/tcp -p 6881:6881/udp \
  -v peerstohttp-data:/downloads peerstohttp:local -dir=/downloads -port-torr=6881
```

> 若把容器内 `-port` 改成 8484（加固版做法），则 `-p` 右侧也必须同步改，否则宿主机访问不到。

### 3.7 docker compose

`docker-compose.yml` 的参数写在 `command:` 列表里，一项一个参数：

```yaml
    command:
      - "-port=80"
      - "-port-torr=6881"
      - "-dir=/downloads"
      - "-cache-capacity=2048"
```

```bash
docker compose up -d
docker compose ps
docker compose logs -f
docker compose restart
docker compose down       # 保留数据卷
docker compose down -v    # 连数据卷删除（下载的数据丢失）
```

老版本 Docker 把 `docker compose` 换成 `docker-compose`。

### 3.8 公网加固编排 `docker-compose.public.yml`

```bash
docker compose -f docker-compose.public.yml up -d --build
docker compose -f docker-compose.public.yml ps
docker compose -f docker-compose.public.yml logs -f caddy
```

启动参数（容器内监听 8484，仅同网络内可达，前面挂 Caddy）：

```yaml
      - "-host=0.0.0.0"
      - "-port=8484"
      - "-port-torr=6881"
      - "-dir=/downloads"
      - "-cache-capacity=5120"
      - "-max-connections=50"
      - "-down-rate=8192"
      - "-up-rate=512"
      - "-no-upnp"
```

> ⚠️ 本快照**缺少该编排引用的 `Caddyfile` 与 `Caddy.Dockerfile`**，直接 `up --build` 会因找不到 Dockerfile 失败。要么补齐这两个文件，要么只保留 `peerstohttp` 服务（`docker compose -f ... up -d peerstohttp`），要么改用 §3.6/§3.7。

---

## 4 日常运维命令

| 动作 | Windows | Linux |
|---|---|---|
| 前台启动 | `.\peerstohttp.exe -port=8484 ...` | `./peerstohttp -port=8484 ...` |
| 后台启动 | `Start-Process ... -WindowStyle Hidden` 或 `.\start.ps1 -Background` | `nohup ... &` |
| 停止 | `Get-Process peerstohttp \| Stop-Process -Force` 或 `.\start.ps1 -Stop` | `kill $(cat peerstohttp.pid)` / `systemctl stop peerstohttp` |
| 查进程 | `Get-Process peerstohttp` | `ps -ef \| grep peerstohttp` |
| 看端口占用 | `Get-NetTCPConnection -LocalPort 8484 -State Listen` | `ss -lntup \| grep -E '8484\|6881'` |
| 日志（前台） | 直接看控制台 | `tail -f peerstohttp.log` |
| 日志（systemd） | — | `journalctl -u peerstohttp -f` |
| 日志（容器） | `docker logs -f peerstohttp` | 同左 |
| 平滑重启 | `.\start.ps1 -Stop; .\start.ps1 -Background` | `systemctl restart peerstohttp` |
| 清空种子记录 | 停服后删 `{dir}/.app.bolt.db` | 同左 |
| 清空下载数据 | 停服后删 `{dir}` 内容 | 同左 |

> **没有**手动删除单个种子的 API，只有自动回收：种子在最后一个请求结束后立即断连，空闲 `-idle-ttl` 秒后被移除；数据库里的 metainfo 是否保留由 `-keep-meta` 决定。
> 想一次性清空全部记录，停服后删 `{dir}/.app.bolt.db`（元数据含 tracker，重启后按需重新创建）。

---

## 5 端口与网络

| 端口 | 协议 | 用途 | 是否必须放行 |
|---|---|---|---|
| `8484`（可改） | TCP | HTTP 服务 | 是，客户端要访问 |
| `6881`（可改） | TCP | BT 连接 | 建议，提升连通率 |
| `6881`（可改） | UDP | BT/uTP + DHT | 建议，**只放 TCP 不放 UDP 效果有限** |

```powershell
# Windows（管理员）
New-NetFirewallRule -DisplayName "peerstohttp BT TCP" -Direction Inbound -Protocol TCP -LocalPort 6881 -Action Allow
New-NetFirewallRule -DisplayName "peerstohttp BT UDP" -Direction Inbound -Protocol UDP -LocalPort 6881 -Action Allow
```

```bash
# ufw
sudo ufw allow 8484/tcp && sudo ufw allow 6881/tcp && sudo ufw allow 6881/udp && sudo ufw reload

# firewalld
sudo firewall-cmd --permanent --add-port=8484/tcp
sudo firewall-cmd --permanent --add-port=6881/tcp
sudo firewall-cmd --permanent --add-port=6881/udp
sudo firewall-cmd --reload
```

- 只开 HTTP 端口也能用（纯靠 DHT 找 peer），但会明显变慢。
- `-port-torr=0`（默认）会让每次启动端口都变，宿主机端口映射失效——**长期运行必须固定**。

---

## 6 启动后验证

按三层递进，状态码就是结论。示例种子为公开测试种 Sintel `08ada5a7a6183aae1e09d831df6748d566095a10`。

```powershell
# ① 服务活着（hash 非法 -> 400 表示服务正常且校验生效）
curl.exe -s -o NUL -w "HTTP %{http_code}`n" --max-time 10 "http://127.0.0.1:8484/list/json/-/-/hash/zzz"

# ② 清单可用（首次要等元数据，最长受 -info-timeout 限制，默认 30s）
curl.exe -s --max-time 60 "http://127.0.0.1:8484/list/m3u/mp4/-/hash/08ada5a7a6183aae1e09d831df6748d566095a10"

# ③ 流可拖动（看到 206 + Content-Range 即成功）
curl.exe -s -o NUL -D - -H "Range: bytes=1000-2023" --max-time 90 `
  "http://127.0.0.1:8484/content/08ada5a7a6183aae1e09d831df6748d566095a10/Sintel/Sintel.mp4"
```

| 结果 | 结论 |
|---|---|
| ① `400` | 服务正常 |
| ① `000` / 连接被拒 | 没启动，或端口/`-host` 不对 |
| ② `200` + JSON | 元数据正常 |
| ② `504` | 元数据超时：DHT/tracker 没找到可用的 peer，或 `-info-timeout` 设得太小 |
| ③ `206` | 边下边播与拖动可用 |
| ③ `200` 且长度 0 | URL 缺「种子名」这一层，用 m3u 里的链接 |
| ③ `500` | 路径与种子内路径不一致 |

> 容器/反代场景把 `127.0.0.1` 换成实际对外地址；`-host=127.0.0.1` 时外部访问必然失败，这是预期行为。

---

## 7 参数处方

| 场景 | 启动参数 |
|---|---|
| **个人局域网自用** | `-port=8484 -port-torr=6881 -dir=C:/bt-data -cache-capacity=0` |
| **低配/小磁盘** | `-port=8484 -port-torr=6881 -dir=/data/bt -cache-capacity=2048 -up-rate=512` |
| **内存敏感（大量长尾种子）** | 基础 + `-idle-ttl=45 -disconnect-ttl=10 -info-timeout=15`：缩短空转宽限期与元数据等待时间，让空闲种子更快回收 |
| **播放器频繁 seek / 续播卡顿** | 基础 + `-disconnect-ttl=60`：放宽断连宽限，避免请求间隙把 peer 连接全部断掉 |
| **公网部署（推荐起手）** | `-host=127.0.0.1 -port=8484 -port-torr=6881 -dir=/var/lib/peerstohttp -cache-capacity=5120 -max-connections=50 -down-rate=8192 -up-rate=512 -no-upnp` |
| **容器内** | `-port=80 -port-torr=6881 -dir=/downloads -cache-capacity=2048 -no-upnp` |
| **连通性差/死种排查** | 上述基础 + `-torr-debug`，并确认 6881 TCP/UDP 均放行 |
| **强干扰网络** | 基础 + `-force-encryption` |
| **需要性能剖析** | 基础 + `-profile`（仅内网，分析完立刻去掉） |
| **日志接入 ELK/Loki** | 基础 + `-json-logs` |

---

## 8 启动失败排查

| 现象 | 原因 | 处理 |
|---|---|---|
| 双击 exe 无反应、无日志 | 缺 3 个运行库 DLL | 把它们放到 exe 同目录，或用 `start.ps1`（把 MinGW bin 前置到 PATH） |
| `listen tcp :80: bind: permission denied` | 80 端口被占用/无权限 | `-port=8484` |
| `first listen: listen tcp4 :6881: bind: Only one usage...` | **已有实例在跑** | 先杀进程再启动（同机只能一个实例） |
| `failed to create dir` / 启动即退出 | `-dir` 无写权限 | 换目录或给运行用户授权（systemd 下 `User=` 用户须拥有该目录） |
| `parse http proxy url` | `-http-proxy` 不是合法 URL | 补全 `http://host:port` |
| `inconsistent vendoring` | `vendor/` 与 `go.mod` 不匹配 | `go build -mod=mod` 或 `go mod vendor` 重建（重建后需按 DEPLOY.md 附录重打补丁） |
| `exec: "gcc": executable file not found` | CGO 找不到编译器 | 装 gcc 并进 PATH，保持 `CGO_ENABLED=1` |
| 参数全部"不生效" | 布尔参数写成了 `-no-dht false` | 改成 `-no-dht` 或 `-no-dht=false` |
| 容器起来即退出 | 参数错 / `-dir` 不可写 | `docker logs peerstohttp` |
| 宿主机连不上容器 | `-port` 与 `-p` 右侧不一致 | 两者对齐 |
| 能列清单但速度为 0 | 6881 未映射或被防火墙拦 | 同时映射 tcp/udp |
| 请求等满 `-info-timeout` 后返回 504 | 没拿到种子元数据：无可用 peer、DHT 被关、或超时太短 | 确认 6881 TCP/UDP 放行且未加 `-no-dht`；必要时调大 `-info-timeout`；死种换 hash |
| 拖动/切集后首字节变慢、CPU 抖动 | `-idle-ttl` 太小，种子被反复回收重建（重建时要按分片数重建元数据） | 调大 `-idle-ttl`（一般 60~120s） |
| 播放中每个缓冲间隙都重新连 peer、速度上不去 | `-disconnect-ttl` 太小（或为 `0`），请求一结束就断连 | 调大 `-disconnect-ttl`（如 60），或临时设 `0` 以外的值与 `0` 做 A/B |
| 播一会儿文件消失 | `-cache-capacity` LRU 淘汰 | 设 `-cache-capacity=0` |

---

## 9 本快照使用须知

1. **无预编译产物**：仓库根目录没有 `peerstohttp.exe`，`start.ps1` 会提示先编译（命令由脚本自动打印）。
2. **`start.ps1` 不透传原始 flag**：只支持 §3.2 表中 7 个参数，需要 `-cache-capacity` 等请直接调 exe。
3. **`docker-compose.public.yml` 引用的 `Caddyfile` / `Caddy.Dockerfile` 在仓库中不存在**，直接 `--build` 会失败，先补齐或只起 `peerstohttp` 服务。
4. **官方镜像与本地构建的默认端口不同**：官方镜像 `winpooh32/peerstohttp:latest` 用程序默认 80，本仓库加固编排显式改成 8484；混用会导致端口映射对不上。
5. **`-profile` 是公网红线**：`/debug/pprof/heap` 可 dump 进程内存，nginx 配置已做 `/debug/` 兜底 404，但最好的做法是不加这个参数。
6. **仓库内两处 vendor 补丁是编译前提**：重置 `vendor/` 后必须重新打补丁，或退回 Go ≤ 1.20 + GCC ≤ 14（详见 DEPLOY.md §10）。
