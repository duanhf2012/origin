# 文件名、滚动与磁盘上限

本例把同一条结构化日志发送到两个独立输出：控制台使用 `info + text`，文件使用
`debug + json`。执行 `run.bat` 或 `./run.sh`；文件位于本示例的 `logs/` 目录。

## 文件名如何确定

配置写的是 `logs/tutorial.log`，启动参数是 `--app-name log-output`，所以实际活动文件为：

```text
log-output-tutorial.log
```

滚动归档使用活动文件 stem 加时间戳，可选 gzip 后缀；不可恢复的 Go Runtime 崩溃写入：

```text
log-output-tutorial-2026-08-08T18-30-00.123.log
log-output-tutorial-2026-08-08T18-30-00.123.log.gz
log-output-tutorial.crash.log
```

`app_name` 只用于这些文件名前缀，不进入每条日志内容。

## 滚动和保留规则

示例配置 `max_size: 1M`：下一条完整日志会使活动文件超过 1 MiB 时，框架先滚动再写；
单条日志不会拆开。`0B` 关闭大小滚动。该配置最终交给只接受整数 MiB 的滚动 Writer，
所以除 `0B` 外必须是 1 MiB 的整数倍；框架拒绝 `512KB`，避免向下截断后实际阈值与配置
不一致。

`by_date: true` 会在 `timezone` 指定时区的自然日变化时滚动，目前不支持按小时滚动。
确实需要小时文件时，应由日志采集系统切分，或者只设置合适的 `max_size`。

`retention` 防止归档无限增长：

- `max_age: 7d` 删除超过七天的归档，`0s` 表示不限时间；
- `max_files: 10` 最多保留十个归档，不包含活动文件，`0` 表示不限数量；
- 两个限制同时存在时，任意一个超限都会清理；
- `compress: true` 让唯一维护协程把普通归档压缩为 gzip。

默认 Handler 的内置默认值是 14 天、30 个归档和开启压缩。生产配置至少应保留一个有限
约束，并结合单文件上限预估磁盘占用。

完整代码见 [`main.go`](./main.go)，逐字段配置注释见
[`config/application.yaml`](./config/application.yaml)。更完整的调用与格式规则见
[日志输出与管理教程](../../../docs/maintenance/v3.1/guides/03.logging.md)。
