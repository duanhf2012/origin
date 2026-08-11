# Gin 与 HTTP Client 纵向切片实施计划

> 目标版本：v3.2  
> 状态：等待核心设计外观确认  
> 实现依据：[Origin Gin 与 HTTP Client 核心设计](../design/Origin%20Gin与HTTP%20Client核心设计.md)

## 1. 范围

本切片实现：

- `sysmodule/ginmodule` 的 Server、ServerConfig、ServerOptions、生命周期、安全边界和固定统计；
- `sysmodule/httpclient` 的可复用 Client、默认 Transport、流式请求和有界响应读取；
- 一个业务 HTTP Module 集中持有 Gin Server 与 HTTP Client 的最小 Example；
- 同服务 HTTP 自调用、Windows/Ubuntu、Race、覆盖率和教程验收。

不实现设计文档“首批不做”的能力，不顺手重构 Application Admin/pprof HTTP Runtime。

## 2. 执行顺序

### 阶段 0：外观冻结

- [ ] 确认包名、Engine 所有权、普通 Handler/ServiceHandler 并发模型和 HTTP Client 所有权；
- [ ] 确认 Server 配置字段、请求 Context 截止时间、默认值以及首批不做范围；
- [ ] 把确认结果写回核心设计，之后实现不得自行扩大公开接口。

### 阶段 1：Gin Server 大切片

- [ ] 加入当前 Go 基线支持的 Gin 依赖；
- [ ] 先实现配置校验、Listener/Serve/Shutdown 状态与失败回滚；
- [ ] 再实现请求容量、Body 上限、panic 边界、可信代理和统计；
- [ ] 实现三段式 `ServiceHandler`、Context 合并、有界响应冻结和错误映射；
- [ ] 覆盖排队取消、Task panic、响应所有权和 Service 串行数据访问；
- [ ] 完成 Server 单元、并发、故障注入和生命周期测试；
- [ ] Review 后冻结 Server 外观。

### 阶段 2：HTTP Client 小切片

- [ ] 实现 `TransportOptions`、独占默认 Transport 和清晰的共享 Transport 所有权；
- [ ] 实现 `Do`、`DoBytes`、总超时、响应上限和空闲连接关闭；
- [ ] 完成连接复用、TLS、取消、超限、Body 关闭和并发测试；
- [ ] Review 后冻结 Client 外观。

### 阶段 3：纵向集成与性能验证

- [ ] 增加同 Service Gin + HTTP Client 自调用集成测试；
- [ ] 验证 `Await` 与 `ServiceHandler` 自调用组合不存在死锁、晚写、任务泄漏或停止悬挂；
- [ ] 添加最小 Benchmark，确认没有每请求新建 Client/Transport 或框架辅助 goroutine；
- [ ] 只处理测试或 Profile 证明的性能问题，不在本阶段扩展功能。

### 阶段 4：教程与整体验收

- [ ] 增加 `examples/14-http`，业务逻辑集中在业务 HTTP Module；
- [ ] 增加 Gin Server 和 HTTP Client 使用指南，完整注释配置字段与建议值；
- [ ] 根 README 的扩展组件表增加 HTTP 章节，不改动 `00`～`12` 基础教程结构；
- [ ] Windows：完整测试、`go vet`、Example 启停；
- [ ] Ubuntu：完整测试、`-race`、覆盖率、Example 启停；
- [ ] 对照代码、配置、Example、教程逐项 Review，形成变更记录和验收报告；
- [ ] 提交到本地 `v3` 分支，不推送远端。

## 3. 防疏漏策略

每个阶段只允许一个未关闭切片。完成代码后立即补齐对应测试，不把测试集中拖到最后。阶段结束必须核对：

1. 公开注释与真实行为一致；
2. 默认值只有一个来源，Config 从运行期默认值生成；
3. 资源所有者、关闭者、超时和错误返回明确；
4. 正常、错误、取消、过载、停止和自调用路径均有测试；
5. 没有为兼容 v2 保留旧 API，也没有超出本次需求的新抽象。

任一阶段发现需要改变已确认外观时，先停止实现、更新核心设计并重新 Review，不能在代码中临时决定。
