# 03 本地 Diagnostics Snapshot

这一组不启动 HTTP。`Application.Diagnostics()` 每次返回一份新的 Full Snapshot，适合进程内
故障记录、退出前留档或业务自定义诊断。快照的所有权层级是：

```text
Snapshot
├── Application：进程身份、生命周期、Admin 与 pprof Listener
├── Runtime：Go 内存、goroutine、GC 等进程运行时数据
├── BufferPool：Application 共享缓冲池
└── Nodes[]：Application 拥有的全部 Node
    └── Services[]：该 Node 拥有的全部本地 Service
```

Application 层不是 Node 快照的重复副本：它描述进程级身份和资源；Node 描述各自的健康、
Transport、Discovery、RPC 和目录；Service 明细只出现在所属 Node 下。一次快照内部属于同一
次采样结果，但它不是事务数据库视图，采集期间各独立运行组件仍可能推进。

`Application.AdminServer` 是当前通用 Admin Listener 状态。Full Schema v2 中的
`Application.DiagnosticsServer` 仅为旧 JSON 消费者保留，已废弃并恒为 `stopped`；不要用它
判断 Admin 是否可用。新代码只读取 `AdminServer`。

运行：

```bash
./examples/10-admin-diagnostics-and-pprof/03-diagnostics-snapshot/run.sh
```

Windows 使用同目录的 `run.bat`。程序会打印一份格式化 JSON；本例没有监听端口。
