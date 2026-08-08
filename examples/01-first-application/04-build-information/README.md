# 编译期构建信息

这个示例不启动业务服务，只编译一个带 BuildTime、Version、Commit 的小程序并执行内置 `version` 命令。

## 运行

执行 `run.bat` 或 `./run.sh`。预期能看到：

```text
version: v3.0.0-demo
commit: demo123
build_time: 2026-08-08T10:00:00+08:00
go_version: go...
```

脚本中的三项值是固定演示数据。生产构建应由 CI 或构建脚本写入真实发布版本、Git Commit 和构建时间；不要在运行时伪造这些数据。运行 `help` 也可观察到非空 BuildTime 出现在帮助头部。

完整的 Windows、Linux/macOS 编译命令和变量说明见：[创建第一个应用](../../../docs/baseline/v3.0/guides/01.first-application.md)。
