// Package buildinfo 提供编译时注入的只读构建信息。
package buildinfo

// 这些变量只允许由链接器通过 -ldflags -X 注入。
// 未注入时保留空值，不在运行时伪造构建信息。
var (
	buildTime string
	version   string
	commit    string
)

// BuildTime 返回编译时注入的构建时间。
func BuildTime() string {
	return buildTime
}

// Version 返回编译时注入的版本号。
func Version() string {
	return version
}

// Commit 返回编译时注入的源码提交标识。
func Commit() string {
	return commit
}
