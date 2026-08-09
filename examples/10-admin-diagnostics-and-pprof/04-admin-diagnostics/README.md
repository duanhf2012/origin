# 04 Admin Diagnostics

Diagnostics 已内置到通用 Admin Listener，不再占用独立 HTTP 端口。启动脚本只传 `--admin`。

本例的 `OnStart` 只演示 `AdminAddress()`：它读取已经绑定的 Admin Listener 地址，不会再调用
`net.Listen`，也不会创建第二个 Diagnostics Server。`--admin 127.0.0.1:6063` 是本例的初始
配置；如果使用 `:0`，应始终使用 `AdminAddress()` 返回的实际端口。

默认请求返回低基数 Summary，适合秒级监控：

```bash
curl -s http://127.0.0.1:6063/admin/v1/diagnostics
```

需要逐个 Service 的执行、Timer、事件等明细时，再按需读取 Full Snapshot：

```bash
curl -s "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full"
```

查询参数只有两种合法形式：省略 query 表示 Summary，或者唯一的 `detail=full` 表示 Full；
例如 `?detail=full&extra=x` 会返回 `400 Bad Request`，不会触发采样。Summary 与 Full 的关系
不是两套数据源：它们共享同一次 Application/Node 诊断模型，只是输出粒度不同。

空闲 Listener 不会周期采样 Diagnostics，但“开启 HTTP”并非绝对零成本：它仍持有一个
Listener、Server 和少量运行资源。只有收到请求后，框架才读取 Go Runtime、聚合
Application/Node/Service/RPC/Timer/Event 等状态并编码 JSON，所以查询不只是读取一块已经
存在的内存。请求越频繁、Node/Service 越多、Full 明细越大，CPU、分配和响应字节越高。

建议把 Summary 接入低频、秒级拉取；Full 只在排障窗口临时请求，不要把 Full 当高频
Metrics。`collect_cost` 是当前机器本次聚合耗时，用于观察趋势，不是跨机器性能承诺。

请求成本大致由三部分组成：读取 Go Runtime，遍历并聚合 Node/Service 运行状态，以及 JSON
编码和网络发送。Admin 空闲时不会周期采样，但 Listener 本身仍占用少量 Server/等待资源，
所以“开启 Admin”不等于“完全没有性能成本”，也不等于“查询只读内存”。

本例只绑定 `127.0.0.1:6063`。不要把包含运行结构和错误状态的诊断响应直接暴露到公网。
