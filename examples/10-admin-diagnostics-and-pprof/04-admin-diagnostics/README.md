# 04 Admin Diagnostics

Diagnostics 已内置到通用 Admin Listener，不再占用独立 HTTP 端口。启动脚本只传 `--admin`。

默认请求返回低基数 Summary，适合秒级监控：

```bash
curl -s http://127.0.0.1:6063/admin/v1/diagnostics
```

需要逐个 Service 的执行、Timer、事件等明细时，再按需读取 Full Snapshot：

```bash
curl -s "http://127.0.0.1:6063/admin/v1/diagnostics?detail=full"
```

空闲 Listener 不会周期采样 Diagnostics，但“开启 HTTP”并非绝对零成本：它仍持有一个
Listener、Server 和少量运行资源。只有收到请求后，框架才读取 Go Runtime、聚合
Application/Node/Service/RPC/Timer/Event 等状态并编码 JSON，所以查询不只是读取一块已经
存在的内存。请求越频繁、Node/Service 越多、Full 明细越大，CPU、分配和响应字节越高。

建议把 Summary 接入低频、秒级拉取；Full 只在排障窗口临时请求，不要把 Full 当高频
Metrics。`collect_cost` 是当前机器本次聚合耗时，用于观察趋势，不是跨机器性能承诺。

本例只绑定 `127.0.0.1:6063`。不要把包含运行结构和错误状态的诊断响应直接暴露到公网。
