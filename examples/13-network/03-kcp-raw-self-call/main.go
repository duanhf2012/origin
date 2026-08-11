// 本示例展示业务 KCP Module 组合 Server 和 Client，并通过回环地址调用自己。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/kcp"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// EchoKCPModule 集中保存 KCP 端点及其业务回调。
//
// 底层 Server 和 Client 作为子 Module 接入生命周期，避免把网络业务散落到 Service。
type EchoKCPModule struct {
	service.Module
	// server 接收回环请求，并把消息原样返回。
	server *kcp.Server
	// client 创建本地 KCP Session 并调用同一 Module 托管的 Server。
	client *kcp.Client
}

// OnInit 构造 KCP 端点，并按“先 Server、后 Client”的顺序加入当前业务 Module。
func (module *EchoKCPModule) OnInit() error {
	serverHandler := network.HandlerFuncs{
		Message: module.onServerMessage,
	}
	// 从完整默认值开始严格覆盖当前 Service 的 kcp.server 配置，拼错字段会阻止启动。
	serverConfig := kcp.DefaultServerConfig()
	if err := module.GetServiceConfigStrict("kcp.server", &serverConfig); err != nil {
		return err
	}
	serverOptions, err := serverConfig.Options(serverHandler)
	if err != nil {
		return err
	}
	// 如需 KCP 包加密，应在这里给 serverOptions.BlockCrypt 注入由安全系统创建的对象。
	server, err := kcp.NewServer(serverConfig.Address, serverOptions)
	if err != nil {
		return err
	}

	clientHandler := network.HandlerFuncs{
		Open:    module.onClientOpen,
		Message: module.onClientMessage,
	}
	// Client 使用独立配置；KCP 双方的帧、MTU、FEC 与加密参数必须兼容。
	clientConfig := kcp.DefaultClientConfig()
	if err := module.GetServiceConfigStrict("kcp.client", &clientConfig); err != nil {
		return err
	}
	clientOptions, err := clientConfig.Options(clientHandler)
	if err != nil {
		return err
	}
	// 启用加密时，clientOptions.Dial.BlockCrypt 必须与 Server 使用兼容实现。
	client, err := kcp.NewClient(clientConfig.Address, clientOptions)
	if err != nil {
		return err
	}

	module.server = server
	module.client = client
	if err := module.AddModule(server); err != nil {
		return err
	}
	return module.AddModule(client)
}

// onServerMessage 处理服务端业务消息，并把收到的内容原样回显。
func (module *EchoKCPModule) onServerMessage(
	_ context.Context,
	session network.Session,
	payload []byte,
) error {
	// payload 只在当前回调返回前有效；Send 会安全复制，所以可以直接回显。
	module.Logger().Info("kcp server received: " + string(payload))
	return session.Send(payload)
}

// onClientOpen 在本地 KCP Session 创建后发送第一条消息。
func (module *EchoKCPModule) onClientOpen(
	_ context.Context,
	session network.Session,
) error {
	// KCP 没有远端握手；OnOpen 只表示本地 Session 已就绪，首条业务应答才证明对端可用。
	return session.Send([]byte("hello through kcp"))
}

// onClientMessage 处理服务端回显，并在验证成功后关闭测试 Session。
func (module *EchoKCPModule) onClientMessage(
	_ context.Context,
	session network.Session,
	payload []byte,
) error {
	module.Logger().Info("kcp client received: " + string(payload))
	session.Close(nil)
	return nil
}

// NetworkService 只提供串行执行边界，并装配项目自己的业务 Module。
type NetworkService struct{ service.Service }

// OnInit 把 KCP 业务 Module 接入当前 Service 生命周期。
func (target *NetworkService) OnInit() error {
	return target.AddModule(&EchoKCPModule{})
}

// init 只把薄 Service 安装到当前 Application。
func init() { app.Setup(&NetworkService{}) }

// main 启动示例 Application。
func main() { app.Start() }
