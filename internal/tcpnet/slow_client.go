package tcpnet

import (
	"github.com/duanhf2012/origin/v3/errs"
)

// slowClientError 保留公共 TransportOverloaded 错误码，同时让上层固定统计能够无字符串判断分类。
type slowClientError struct{}

func (slowClientError) Error() string { return "tcpnet: 发送队列持续高水位，关闭慢连接" }
func (slowClientError) Code() errs.Code {
	return errs.CodeTransportOverloaded
}
func (slowClientError) Is(target error) bool {
	return target == errs.ErrTransportOverloaded
}
func (slowClientError) SlowClient() bool { return true }
