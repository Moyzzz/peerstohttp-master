# peerstohttp 文档

面向不同读者分成三组。第一次接触项目建议先读根目录的 [README.md](../README.md)，再按需往下看。

## 面向开发者

| 文档 | 内容 |
|---|---|
| [architecture.md](architecture.md) | 分层结构、一次 `/content` 的完整数据流、存储与持久化、全局约束（连接数、做种） |
| [torrent-lifecycle.md](torrent-lifecycle.md) | 种子生命周期：引用计数、两段式回收、两个定时器、状态机、锁顺序、失败路径 |
| [content-streaming.md](content-streaming.md) | `/content` 读取链路、`ctxReadSeeker` 的成因与实现、Range/206 归属、排障日志 |

## 面向接口使用方

| 文档 | 内容 |
|---|---|
| [API.md](API.md) | 3 个 HTTP 端点、路径参数、响应格式、状态码、Range 语义 |

## 面向运维

| 文档 | 内容 |
|---|---|
| [OPS.md](OPS.md) | 启动方式、全部启动参数（23 个）、端口与防火墙、启动后验证、故障排查 |
| [../DEPLOY.md](../DEPLOY.md) | Windows / Linux / Docker Compose 部署，含编译依赖与 vendor 补丁说明 |

## 版本历史

| 版本 | 说明 |
|---|---|
| [releases/v0.1.0.md](releases/v0.1.0.md) | 首个正式版：种子生命周期管理、读取取消、断连宽限 |

## 约定

- 正文中文，文件名 ASCII（避免 Windows 与 URL 编码问题）。
- 文档中的参数名、默认值必须与 `settings/settings.go` 一致；代码位置以文件路径 + 符号名标注，不写死行号。
- 涉及 vendored 依赖的行为时，注明是库的行为还是本仓库的封装。
