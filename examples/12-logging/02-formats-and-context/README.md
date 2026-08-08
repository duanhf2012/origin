# 输出格式与归属字段

运行 `run.bat` 或 `./run.sh`。控制台使用 `text`，会看到类似：

```text
2026-08-08T18:20:31.123 INFO [game-1] 02-formats-and-context/main.go:24 player loaded player_id=10001 player_name="Boyce Duan" online=true position={"x":10,"y":20}
```

文件使用 `json`，实际路径是
`logs/format-context-origin.log`（完整路径位于当前示例目录），内容类似：

```json
{"time":"2026-08-08T10:20:31.123Z","level":"info","node_id":"game-1","service_name":"FormatService","caller":"02-formats-and-context/main.go:24","msg":"player loaded","player_id":10001,"player_name":"Boyce Duan","online":true,"position":{"x":10,"y":20}}
```

关键规则：

- text 时间使用本地时间且不附加 `+0800`；JSON 时间统一使用 UTC `Z`，方便日志平台处理。
- `context_fields` 必须分别放在 `console` 和 `file` 下。`node_id` 与 `service_name` 可独立
  开关；不配置时两者默认都显示。
- `app_name` 不进入日志内容，只作为文件名前缀。配置 `origin.log`、启动参数
  `--app-name format-context`，实际活动文件就是 `format-context-origin.log`。
- 文本字段使用 `key=value`；带空白的字符串自动加引号，复杂值保持紧凑 JSON。

修改 [`config/application.yaml`](./config/application.yaml) 中四个归属开关后重新运行，可直接
观察两个输出端彼此独立的效果。完整代码见 [`main.go`](./main.go)。
