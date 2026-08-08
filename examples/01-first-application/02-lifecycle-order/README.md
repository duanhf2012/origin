# 生命周期顺序

这个示例只关注一个 Node 内多个 Service 的依赖式启动与停止顺序。YAML 的 `services` 列表按正序启动；停止时严格倒序，因此先启动的基础 Service 可以最后释放资源。

## 关键文件

- `config/application.yaml`：`FirstService` 在 `SecondService` 之前声明。
- `main.go`：两个 Service 都输出 `OnStart` 和 `OnStop` 日志。

## 运行与观察

执行 `run.bat` 或 `./run.sh`，再按 `Ctrl+C`。预期顺序为：`FirstService` 启动、`SecondService` 启动、`SecondService` 停止、`FirstService` 停止。

可交换 YAML 中的两个名称后再次运行；不要依赖 Go 文件的书写顺序，真正决定顺序的是最终的 Node 配置。

对应教程：[创建第一个应用](../../../docs/baseline/v3.0/guides/01.first-application.md)。
