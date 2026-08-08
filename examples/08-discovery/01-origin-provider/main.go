// 本示例通过 YAML 选择 Origin 内置 Provider，并复用统一发现监听 Service。
package main

import (
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialwatcher"
)

// app 从配置中创建 DiscoveryService 和普通业务 Node。
var app = application.New()

// init 只登记监听 Service；内置 DiscoveryService 由框架按配置装配。
func init() { app.Setup(&tutorialwatcher.Service{}) }

// main 启动 Application，并由配置选择 origin Provider。
func main() { app.Start() }
