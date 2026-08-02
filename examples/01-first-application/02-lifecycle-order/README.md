# 生命周期顺序

在同一个 Node 中，配置中的 `services` 顺序就是启动顺序；停止时严格倒序。运行后按 `Ctrl+C`，比较四行日志即可观察顺序。

```text
run.bat
```

```bash
./run.sh
```

对应教程：[创建第一个应用](../../../docs/baseline/v3.0/guides/01-first-application.md)。
