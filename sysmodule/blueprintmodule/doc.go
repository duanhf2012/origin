// Package blueprintmodule 将 OriginBlueprint Go 引擎接入 Origin Service 生命周期与串行工作协程。
//
// 普通业务优先使用 Module.Run 执行一次性蓝图，使用 Instance 管理战斗、副本或 AI 会话等长期图身份。
// 自定义异步节点通过 BaseExecNode.Yield 暂停，并在外部回调中仅使用 YieldHandle.Resume 或 ResumeTo；恢复
// 后的蓝图节点由 Module 投递回所属 Service 工作协程。
package blueprintmodule
