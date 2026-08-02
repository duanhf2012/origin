// 本示例通过 YAML 选择 etcd Provider，业务监听代码与 Origin Provider 完全相同。
package main

import (
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialwatcher"
)

// app 会读取 etcd endpoints、TTL 和 local_network 配置。
var app = application.New()

// init 登记与 Provider 无关的统一监听 Service。
func init() { app.Setup(&tutorialwatcher.Service{}) }

// main 启动 Application；运行前需先启动 README 中说明的 etcd 依赖。
func main() { app.Start() }
