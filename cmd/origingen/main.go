// Command origingen 生成 Origin 框架的静态代码。
package main

import (
	"fmt"
	"os"

	"github.com/duanhf2012/origin/v3/internal/rpcgen"
)

// main 执行最终命令入口；生成失败时只在所有内部清理完成后决定进程退出码。
func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 解析保持精简的子命令外观；具体扫描和文件事务全部由 rpcgen 负责。
func run(arguments []string) error {
	if len(arguments) == 0 || arguments[0] != "rpc" {
		return fmt.Errorf("用法: origingen rpc [--check] <packages...>")
	}
	options := rpcgen.Options{}
	for _, argument := range arguments[1:] {
		if argument == "--check" {
			options.Check = true
			continue
		}
		options.Patterns = append(options.Patterns, argument)
	}
	return rpcgen.Run(options)
}
