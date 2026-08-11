# HTTP Client 使用指南

`httpclient.Client` 是代码持有、可并发复用的 HTTP 客户端，不是 Module，也没有 YAML 配置。通常为一个
上游或一组相同连接策略的上游长期保存一个 Client；不要为每次请求新建 Client 或 Transport。

## 1. 创建并复用 Client

```go
options := httpclient.DefaultOptions()
client, err := httpclient.New(options)
if err != nil {
    return err
}
```

`Transport` 留空时，每个 Client 创建自己的连接池。需要多个 Client 共享连接池时显式创建并注入：

```go
transportOptions := httpclient.DefaultTransportOptions()
transportOptions.MaxConnsPerHost = 128
transport, err := httpclient.NewTransport(transportOptions)
if err != nil {
    return err
}

options := httpclient.DefaultOptions()
options.Transport = transport
client, err := httpclient.New(options)
```

共享 Transport 的所有 Client 都会受 `CloseIdleConnections` 影响，调用方必须统一决定关闭时机。

## 2. 函数、回调参数与执行协程

这里的“调用 goroutine”是实际调用该函数的 goroutine，不承诺固定 ID。一个 Client 可以被多个 goroutine
同时调用，因此所有注入的函数和对象即使在单次请求内顺序执行，也必须支持不同请求之间的并发调用。

| 函数或函数参数 | 实际执行位置 | 所有权与并发规则 | Service 中的使用决定 |
| --- | --- | --- | --- |
| `DefaultOptions()`、`DefaultTransportOptions()` | 当前调用 goroutine | 只创建值，不启动 goroutine，不执行网络操作 | 可在 `OnInit` 调用 |
| `New(options)` | 当前调用 goroutine | 同步校验并保存 `Transport`、`CheckRedirect`、`Jar`；此时不执行这些对象 | 可在 `OnInit` 调用 |
| `NewTransport(options)` | 当前调用 goroutine | 同步校验并克隆 `TLSConfig`；此时不拨号，也不执行 Proxy/TLS 回调 | 可在 `OnInit` 调用 |
| `Client.Do(request)` | 调用 `Do` 的 goroutine 同步等待结果；标准 Transport 另有内部拨号和连接读写 goroutine | 成功后 `Response.Body` 归调用方读取和关闭；多个调用方可并发 | 普通 goroutine 直接调用；Service Task 内必须放入 `Await` |
| `Client.DoBytes(request)` | 调用 goroutine 执行 `Do`、有界读取和 `Body.Close` | 返回前已关闭标准 Body；返回的 Header/Body 是调用方私有快照 | 普通 goroutine 直接调用；Service Task 内必须放入 `Await` |
| `Options.CheckRedirect(request, previous)` | 发起当前 `Do` 的调用 goroutine，在下一次跳转前执行 | 同一 Client 的不同请求可并发进入；不能访问 Service 串行状态 | 只使用请求级或并发安全数据 |
| `Options.Jar` 的 `Cookies`/`SetCookies` | 发起当前 `Do` 的调用 goroutine，在请求发送前或响应返回后执行 | Jar 会被多个调用并发使用；实现必须并发安全 | 不能访问 Service 串行状态 |
| 自定义 `RoundTripper.RoundTrip(request)` | 由 `http.Client.Do` 在调用 goroutine 中进入；实现可自行创建内部 goroutine | 必须支持并发调用，并遵守标准 RoundTripper 的 Request/Response Body 契约 | 不能假定持有 Service 执行权 |
| `TransportOptions.Proxy(request)` | 标准 Transport 的当前请求传输流程；不同请求可并发调用 | 必须并发安全；默认读取环境代理设置 | 不能访问 Service 串行状态 |
| `TLSConfig` 的证书与校验回调 | `net/http`/TLS 管理的握手 goroutine | 可能并发执行；只能访问不可变或并发安全数据 | 不能访问 Service 串行状态 |
| `Client.CloseIdleConnections()` | 当前调用 goroutine | 只关闭底层空闲连接，不中断活动请求；之后 Client 仍可继续使用 | 通常在应用停止或上游配置切换时调用 |
| `module.Await(ctx, fn)` 中的 `fn(waitCtx)` | 原 Service Task goroutine；调用 `fn` 前已释放 Service 执行权 | `fn` 返回并按 FIFO 恢复后才重新持有 Service 执行权 | `Do`/`DoBytes` 应在这个 `fn` 内调用 |

最关键的判断是：`Await` 的等待函数仍在原 Task goroutine，但此时已经释放 Service 串行执行权。因此等待
HTTP I/O 不会阻塞其他 Service Task，也不能在等待函数里直接读写只允许 Service 串行访问的业务数据。

## 3. 在普通 goroutine 中调用

```go
request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
if err != nil {
    return err
}
response, err := client.DoBytes(request)
if err != nil {
    return err
}
if response.StatusCode != http.StatusOK {
    return fmt.Errorf("upstream status: %d", response.StatusCode)
}
```

`DoBytes` 不把 `4xx/5xx` 转换为 Go error；业务必须根据 `StatusCode` 解释上游协议。

## 4. 在 Service Task 中调用

```go
var response httpclient.Response
err := module.Await(ctx, func(waitCtx context.Context) error {
    // 当前等待函数运行在原 Task goroutine，但已经释放 Service 执行权。
    request, err := http.NewRequestWithContext(waitCtx, http.MethodGet, targetURL, nil)
    if err != nil {
        return err
    }
    response, err = module.client.DoBytes(request)
    return err
})
if err != nil {
    return err
}

// Await 返回后已重新取得 Service 执行权，可以安全更新串行业务状态。
module.lastStatus = response.StatusCode
```

调用同一个 Service 的 Gin Safe 路由时必须采用以上写法：原 Task 释放执行权后，HTTP 入口投递的 Safe Task
才能执行。直接在 Service Task 中阻塞调用自己的 Safe 路由会造成自调用死锁。

## 5. 请求与响应所有权

- Client 不修改调用方的 URL、Header 或业务字段，也不自动添加鉴权、Content-Type 或 Trace Header；
- `Do` 沿用标准库语义：请求 Body 由标准 Client 负责关闭，成功响应 Body 由调用方读取并关闭；
- `DoBytes` 自动读取并关闭响应 Body；返回的 Header 是克隆，Body 完全归调用方；
- `DoBytes` 的大小上限作用于透明 gzip 解压后的 Body；超过上限返回
  `ErrResponseBodyTooLarge`，不会返回可误用的部分响应；
- 读取错误、Body 关闭错误会保留在 error 链中；可用 `errors.Is` 判断；
- Request Context 可以提供比 Client 总超时更短的 Deadline。活动请求取消依赖各自 Context，不依赖
  `CloseIdleConnections`。

## 6. Client 默认值

| 字段 | 默认值 | 调整建议 |
| --- | ---: | --- |
| `Timeout` | `30s` | 覆盖连接、重定向和读取 Body；按上游 SLO 收紧 |
| `MaxResponseBodySize` | `4MiB` | 只作用于 `DoBytes`；按真实协议收紧，大响应使用 `Do` 流式处理 |
| `Transport` | 私有默认 Transport | 只有明确需要共享连接池时才注入 |
| `CheckRedirect` | 标准最多 10 次 | 服务间禁止跳转时返回 `http.ErrUseLastResponse` |
| `Jar` | `nil` | 无状态服务间调用通常不需要 Cookie Jar |

## 7. Transport 默认值

| 字段 | 默认值 | 说明 |
| --- | ---: | --- |
| `DialTimeout` | `5s` | DNS/TCP 单次建连预算 |
| `DialKeepAlive` | `30s` | TCP KeepAlive 探测周期，不是连接池空闲时间 |
| `TLSHandshakeTimeout` | `10s` | TLS 握手预算 |
| `ResponseHeaderTimeout` | `15s` | 写完请求后等待响应 Header 的预算 |
| `IdleConnTimeout` | `90s` | HTTP Keep-Alive 空闲连接保留时间 |
| `MaxIdleConns` | `128` | 全部目标合计的空闲连接上限 |
| `MaxIdleConnsPerHost` | `16` | 单目标保留的空闲连接上限 |
| `MaxConnsPerHost` | `64` | 单目标拨号中、活动和空闲连接总上限 |
| `MaxResponseHeaderBytes` | `1MiB` | 单响应 Header 上限 |
| `Proxy` | `http.ProxyFromEnvironment` | 遵循 `HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`；设为 nil 可禁用 |
| `TLSConfig` | `nil` | 使用系统根证书；自签证书应注入 Root CA |

默认 Transport 固定启用 HTTP/2 尝试、透明 gzip 和一秒 `Expect: 100-continue` 预算。构造器拒绝
`InsecureSkipVerify=true`；自签证书应加入正确的 Root CA，而不是关闭证书校验。

框架不提供自动重试、退避、熔断、默认 Header、Base URL 或 JSON/PB 便捷方法。这些行为涉及业务幂等、
鉴权和错误协议，应由具体上游 Client 明确实现。
