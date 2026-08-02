# 读取 Diagnostics 快照

应用启动时读取一次不可变快照，并输出 Application 状态、Node 数量和 Go goroutine 数量。

```text
run.bat
```

业务长期监控应定期采集并转换快照，而不是保存旧快照引用当作实时状态。
