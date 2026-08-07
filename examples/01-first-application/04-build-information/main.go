// 本示例只演示由编译器注入的构建信息和内置 version 命令。
package main

import "github.com/duanhf2012/origin/v3/application"

// version 命令不需要加载业务配置、创建 Node 或登记 Service。
var app = application.New()

// main 让 Application 提供统一的 version 与 help 命令外观。
func main() { app.Start() }
