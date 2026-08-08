# 控制台、文件与滚动日志

本例把同一条结构化日志送到两个独立输出：控制台使用 `info + text`，文件使用
`debug + json`。运行后可直接对比终端和生成的
`logs/tutorial.log`；`logs/` 由框架自动创建，不需要提前建目录。

## 运行与观察

执行 `run.bat` 或 `./run.sh`。终端看不到 `debug message for file output`，但 JSON 日志文件中
可以看到；两边都会自动带上 `app_name`、`node_id`、`service_name` 和调用位置。

配置的 `max_size: 1M` 表示 1 MiB。活动文件在“下一条完整日志会超过阈值”时先滚动，单条
日志不会被拆开；`by_date: true` 还会在指定时区的自然日变化时滚动。归档使用时间戳命名，
随后按 `max_age`、`max_files` 清理，并在 `compress: true` 时压缩为 gzip。
`max_size` 先按字节解析，再转换为运行时的整数 MiB，因此除 `0B` 外必须是 1 MiB 的整数倍；
`512KB` 这类值会被拒绝，避免实际滚动阈值因截断而与配置不一致。

## 重要边界

- `mode: async` 的普通日志在固定队列满时会按级别累计丢弃，避免无限占用内存；
  `ErrorStack` 使用有界可靠写路径。`sync` 会等待每条写完，适合测试和本地观察。
- 未配置 `enabled` 时，控制台默认开启、文件默认关闭；文件必须显式写 `enabled: true`。
  控制台和文件可以使用不同 `level`、`format`；当前级别是 `debug/info/warn/error`，格式是
  `text/json`。两个输出不能同时关闭。
- 文件启用后还会安装同目录的 `tutorial.crash.log`，用于 Go Runtime 无法恢复的进程崩溃；
  它不替代正常错误日志。
- 相对 `path` 取决于启动工作目录。生产环境应使用明确、可写并由部署系统持久化的路径。
- 删除本例生成的 `logs/` 不影响源码；再次运行会自动创建。

对应教程：[配置应用](../../../docs/baseline/v3.0/guides/02-configuration.md)。
