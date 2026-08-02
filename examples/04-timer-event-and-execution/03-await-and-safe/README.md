# Await、RunSafe 与 GoSafe

`Await` 用于等待外部操作并临时释放当前 Service 执行权；`RunSafe` 为当前 goroutine 建立 panic 边界；`GoSafe` 启动带 panic 保底的后台 goroutine。

```text
run.bat
```

等待三行业务输出后按 `Ctrl+C`。示例没有故意 panic；请不要把 `RunSafe`、`GoSafe` 当成 Service 状态并发访问许可。
