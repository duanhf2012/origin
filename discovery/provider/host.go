package provider

import (
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// Host 是 Provider 唯一能够影响框架发现状态的受限能力。
//
// 零值无效。回调字段保持私有，第三方 Provider 不能替换框架行为。
type Host struct {
	setTTL          func(time.Duration) error
	replaceSnapshot func(Snapshot) error
	report          func(Report)
}

// NewHost 为框架集成层创建受限 Host。
//
// 业务项目通常不需要调用；Provider 只消费 Context 中已经构造完成的值。
func NewHost(
	setTTL func(time.Duration) error,
	replaceSnapshot func(Snapshot) error,
	report func(Report),
) Host {
	return Host{
		setTTL:          setTTL,
		replaceSnapshot: replaceSnapshot,
		report:          report,
	}
}

// SetTTL 在首次快照或状态上报前登记一次旧快照保留 TTL。
//
// 重复设置相同值幂等；不同值由框架返回配置错误。
func (host Host) SetTTL(ttl time.Duration) error {
	if host.setTTL == nil {
		return errs.NewMessage(errs.CodeInternal, "Provider Host 未初始化")
	}
	return host.setTTL(ttl)
}

// ReplaceSnapshot 提交一份完整权威快照。
func (host Host) ReplaceSnapshot(snapshot Snapshot) error {
	if host.replaceSnapshot == nil {
		return errs.NewMessage(errs.CodeInternal, "Provider Host 未初始化")
	}
	return host.replaceSnapshot(snapshot)
}

// Report 提交 Provider 健康状态；零值 Host 安全忽略，避免错误清理路径 panic。
func (host Host) Report(report Report) {
	if host.report != nil {
		host.report(report)
	}
}
