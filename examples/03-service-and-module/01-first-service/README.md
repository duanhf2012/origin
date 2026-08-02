# 新增一个业务 Service

这个示例展示创建 Service 的固定最小步骤：嵌入 `service.Service`、按需实现生命周期回调、通过 `app.Setup` 登记类型、在 Node 的 `services` 中写入实际类型名。

## 关键代码

`main.go` 中的 `InventoryService` 在 `OnStart` 打印准备完成日志。YAML 中的 `InventoryService` 不是任意别名，而是登记类型默认推导出的实际 Service 名。

## 运行与练习

执行 `run.bat` 或 `./run.sh`，观察 Service 归属的 Node ID 与 Service 名。可复制该类型创建第二个业务 Service，再把它加入 YAML 列表；如果遗漏 `app.Setup`，启动会明确报告未知类型。

对应教程：[Service 与 Module](../../../docs/baseline/v3.0/guides/03-service-and-module.md)。
