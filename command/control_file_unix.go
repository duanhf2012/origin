//go:build linux || darwin

package command

// isTransientControlResponseReadError 在 Unix 控制文件路径上不放宽任何读取错误。
//
// 当前只有 Windows sharing/lock violation 具有已经复现并确认的瞬时语义；其他平台若发现
// 独立问题，必须先取得确定性证据，不能复用字符串匹配或宽泛重试。
func isTransientControlResponseReadError(error) bool {
	return false
}
