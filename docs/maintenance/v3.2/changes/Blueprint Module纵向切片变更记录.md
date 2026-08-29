# Blueprint Module 纵向切片变更记录

> 日期：2026-08-12
> 目标版本：v3.2
> 引擎依赖：`github.com/duanhf2012/OriginBlueprint v0.1.6`

## 1. 交付内容

- 新增 `sysmodule/blueprintmodule`，把 OriginBlueprint Go 引擎接入 Origin Module 生命周期和 Service
  串行工作协程；不复制 Registry、Compiler、VM 或热加载实现。
- 提供 `New/Setup/RegisterNodes`、一次性 `Module.Run`、长期 `Instance`、受限 `Execution`、
  `OnComplete`、显式单飞 `Reload`、Trace、结构化诊断和轻量统计。
- 用 `*Instance` 替代 v2 裸 graph ID；支持诊断 Key、幂等 Close，并由 Module 停止兜底回收。
- 自定义 Dispatcher 保证首次节点在调用方 Service task 内执行，Yield 恢复后的节点通过同一 Service
  有界 FIFO 执行。包装层没有隐藏 worker pool 或第二层队列。
- 热加载在 `Await` 等待阶段读取、解析和编译，全量成功后原子发布；活动 Execution 固定旧快照，同一
  Instance 的下一次执行读取新图。
- 新增完整 Battle Example 和使用指南，覆盖一次性/长期执行、异步 RPC 风格节点、完成回调、热加载、
  配置、协程边界、所有权、过载与排错。

## 2. Review 后的调整

- 配置字段遵循 Origin 配置映射约定，使用显式 `json` Tag 将 `NodeDir/GraphDir` 固定为
  JSON/YAML 共用的 `node_dir/graph_dir`，不读取 `yaml` Tag。
- 引擎关闭错误通过 `ErrBlueprintClosed` 别名直接暴露，业务无需额外导入底层引擎包。
- 补充 Service 队列满测试：Resume 提交失败不会消费 Yield 句柄，可在有界策略内重试；
  `OnComplete` 登记失败不会取消 Execution，并允许容量恢复后重新登记。
- 教程明确异步蓝图的 Scheduler `max_tasks` 至少为 2，并应按并发量留余量；不提供自动无限重试。
- Benchmark 未显示包装层存在需要对象池解决的瓶颈，因此没有增加内存池、Execution 池或额外消息队列。

## 3. 兼容性与范围

- 不兼容 Origin v2 Blueprint API、裸 ID、旧文件格式或历史命名。
- 不自动监听文件、不周期 Reload、不提供跨进程 Instance Registry，也不包装数据库、RPC、Timer 等业务
  节点；这些节点由项目按实际协议实现。
- 外观接口以当前代码为真相；设计、教程和 Example 已按最终代码同步复核。
