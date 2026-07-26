package main

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/buildinfo"
	"github.com/duanhf2012/origin/v3/errs"
)

// main 展示 M0 构建信息和稳定错误码的最小使用方式。
func main() {
	// 先输出示例名称，便于直接运行时确认当前可执行程序。
	fmt.Println("Origin v3 basic example")
	// 读取由构建脚本通过 ldflags 注入的三项只读构建信息。
	fmt.Printf(
		"build: version=%q commit=%q time=%q\n",
		buildinfo.Version(),
		buildinfo.Commit(),
		buildinfo.BuildTime(),
	)

	// 创建一个带稳定错误码和公开消息的示例错误。
	err := errs.NewMessage(errs.CodeInvalidArgument, "player ID is empty")
	// 通过 CodeOf 读取错误码，业务无需依赖错误字符串进行判断。
	fmt.Printf("error: code=%d message=%q\n", errs.CodeOf(err), err)
}
