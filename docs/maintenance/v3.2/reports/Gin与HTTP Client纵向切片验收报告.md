# Gin 与 HTTP Client 纵向切片验收报告

> 日期：2026-08-11
> Windows：Go 1.26.5，windows/amd64
> Ubuntu：Go 1.26.5，linux/amd64，Linux 7.0.0-28-generic
> 结论：Gin HTTP Module、HTTP Client、同 Service 自调用与教程验收通过

## 已验证能力

- Gin 配置默认值、严格转换、私有 Engine、真实监听、普通路由、所有 Method 包装、Group、404/405、
  请求 Body/Header/并发上限、可信代理、panic 边界、统计和确定性停止；
- SafeContext 请求快照、JSON 绑定、Keys 传递、响应冻结、Header 过滤、大小限制、Safe Middleware
  `Next/Abort`、SafeGroup 嵌套、Service 串行状态访问和 panic 收敛；
- Safe 请求排队取消、运行中取消、Deadline、队列过载、错误映射、停止中的在途请求以及取消后不晚写；
- HTTP Client 私有/共享 Transport、顺序与并发连接复用、连接总量、代理、重定向、Cookie Jar、TLS
  默认拒绝和私有 CA、响应 Header 超时/大小、透明 gzip 解压后 Body 上限；
- `Do` 的流式 Body 所有权，`DoBytes` 的精确边界、Header/Body 快照、读取/关闭组合错误、4xx/5xx
  普通响应和空闲连接关闭；
- 同 Service 自调用的成功、业务错误、取消、过载和停止路径；
- Gin、HTTP Client 与 TCP/WebSocket/KCP 教程中的公开函数、函数参数和扩展回调执行协程说明。

## Windows 结果

以下门禁全部通过：

```text
go test ./... -count=1 -timeout=240s
go vet ./...
go run ./cmd/origingen rpc --check ./...
go build ./...
Gin、HTTP Client 与 network 定向 shuffle 测试
Gin 与 HTTP Client 定向 Race 测试
HTTP Example 真实启动、自调用和停止
```

重点包覆盖率：

```text
sysmodule/ginmodule：96.5%
sysmodule/httpclient：96.7%
```

Gin 的路由方法族、Safe 路由方法族、配置转换、请求边界、错误映射和绝大多数 SafeContext 方法达到
100%。HTTP Client 的公开 `Do`、`DoBytes`、`CloseIdleConnections`、默认值、Options 校验和
`NewTransport` 达到 100%。剩余未覆盖语句主要是 nil/内部不变量、意外 Serve 退出日志、构造器使用固定
有效默认值时不可达的失败分支，以及难以稳定制造的操作系统拨号/握手失败分支；相关公开风险已经由
端口占用、Context 取消、证书失败/成功、响应超时和真实双平台网络测试覆盖。

复用 Client/Transport 的 Windows 基线（5 次）：

```text
233,299～270,364 ns/op
6,887～6,905 B/op
80 allocs/op
```

该基准包含本机 `httptest` 与标准 `net/http` 开销，用于防止退化为每请求创建 Client/Transport，
不代表生产网络延迟。当前没有 Profile 证据支持增加内存池或自定义连接队列。

## Ubuntu 结果

当前工作树以临时 vendor 包上传到唯一 `/tmp/origin-v3-http-*` 目录，结束后已清理，没有修改远端既有
仓库或 Go 安装。以下门禁全部通过：

```text
go test -mod=vendor ./... -count=1 -timeout=300s
go vet -mod=vendor ./...
go run -mod=vendor ./cmd/origingen rpc --check ./...
go build -mod=vendor ./...
go test -mod=vendor -race ./... -count=1 -timeout=600s
```

Ubuntu 覆盖率为 `sysmodule/ginmodule 96.0%`、`sysmodule/httpclient 96.7%`。配置驱动的 HTTP Example
真实启动后，`/health` 返回 `{"status":"ok"}`；Timer 经 `Await` 调用自身 SafePOST 后，SafeGET 返回
`{"id":"42","name":"Origin"}`；`stop --app-name gin-safe-self-call` 使进程正常退出。

## 文档与并发决策验收

五份组件指南均明确区分“调用函数的 goroutine”和“函数值参数之后实际执行的 goroutine”。使用者可以
直接判断哪些回调能访问 Service 串行数据，哪些只能访问不可变或并发安全数据，并能识别三类关键边界：

1. Gin 普通 Middleware/Handler 在请求 goroutine，Safe Middleware/Handler 在 Service 工作协程；
2. HTTP `Await` 等待函数仍在原 Task goroutine，但调用期间已经释放 Service 执行权；
3. 网络 Handler/Decode 在 Service，Dialer 的等待在调用 goroutine，Codec Encode、Origin/TLS 和 KCP
   加密回调按各自调用或 I/O 路径执行。

## 验收结论与范围

当前公开外观、配置、实现、测试、Example 和教程一致，没有已知未解释失败。测试覆盖了主要正常、错误、
取消、过载、panic、并发和停止路径，但不宣称能够数学证明“绝对无 Bug”；生产发布前仍应按真实鉴权、
流量、上游 SLO、证书和代理拓扑执行项目级压测与故障演练。
