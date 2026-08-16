package service

import "time"

// NodeRuntime 是业务 Service 和 Module 可以访问的最小所属 Node 外观。
//
// 它只暴露稳定身份和游戏逻辑时间，不允许业务代码直接控制 Node 生命周期、遍历其他
// Service 或访问内部网络资源。SetTime 和 AddTime 影响当前 Node 的全部业务 Timer；不会
// 修改操作系统时间，也不会影响其他 Node 或 RPC、Await 等基础设施 Deadline。
type NodeRuntime interface {
	// ID 返回当前 Node 的稳定身份。
	ID() string
	// SessionID 返回当前 Node 本次进程启动的非零随机会话标识。
	SessionID() uint64
	// Now 返回当前 Node 的游戏逻辑时间。
	Now() time.Time
	// SetTime 把当前 Node 的游戏逻辑时间设置到 value。
	SetTime(value time.Time) error
	// AddTime 在当前 Node 的游戏逻辑时间偏移上增加 delta；负数表示向后调整。
	AddTime(delta time.Duration) error
}
