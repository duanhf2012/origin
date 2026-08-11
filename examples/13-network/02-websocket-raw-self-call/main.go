// 本示例展示业务 WebSocket Module 组合 Server 和 Client，并通过回环地址调用自己。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/websocket"
)

// app 是当前示例唯一的 Application。
var app = application.New()

// EchoWebSocketModule 集中保存 WebSocket 端点及其业务回调。
//
// 底层 Server 和 Client 作为子 Module 接入生命周期，避免把网络业务散落到 Service。
type EchoWebSocketModule struct {
	service.Module
	// server 接收回环请求，并把消息原样返回。
	server *websocket.Server
	// client 主动连接同一 Module 托管的 Server，验证服务调用自己的入口。
	client *websocket.Client
}

// OnInit 构造 WebSocket 端点，并按“先 Server、后 Client”的顺序加入当前业务 Module。
func (module *EchoWebSocketModule) OnInit() error {
	// Server 的 Handler 直接绑定业务 Module 方法，使网络逻辑集中在当前类型。
	serverHandler := network.HandlerFuncs{
		Message: module.onServerMessage,
	}
	// 从完整默认值开始严格覆盖当前 Service 的 websocket.server 配置。
	serverConfig := websocket.DefaultServerConfig()
	if err := module.GetServiceConfigStrict("websocket.server", &serverConfig); err != nil {
		return err
	}
	serverOptions, err := serverConfig.Options(serverHandler)
	if err != nil {
		return err
	}
	// CheckOrigin、TLSConfig 等运行期安全对象应在这里按项目策略注入。
	server, err := websocket.NewServer(serverConfig.Address, serverOptions)
	if err != nil {
		return err
	}

	// Client 复用相同业务 Module 的状态和日志，并在连接后发起自调用。
	clientHandler := network.HandlerFuncs{
		Open:    module.onClientOpen,
		Message: module.onClientMessage,
	}
	// Client 使用包含完整路径的 URL，并保持与 Server 相同的消息类型和心跳契约。
	clientConfig := websocket.DefaultClientConfig()
	if err := module.GetServiceConfigStrict("websocket.client", &clientConfig); err != nil {
		return err
	}
	clientOptions, err := clientConfig.Options(clientHandler)
	if err != nil {
		return err
	}
	client, err := websocket.NewClient(
		clientConfig.URL,
		clientOptions,
	)
	if err != nil {
		return err
	}

	// 保存端点供后续业务查询，并通过子 Module 关系交给框架管理生命周期。
	module.server = server
	module.client = client
	if err := module.AddModule(server); err != nil {
		return err
	}
	return module.AddModule(client)
}

// onServerMessage 处理服务端业务消息，并把收到的内容原样回显。
func (module *EchoWebSocketModule) onServerMessage(
	_ context.Context,
	session network.Session,
	payload []byte,
) error {
	// payload 只在当前回调返回前有效；Send 会安全复制，所以可以直接回显。
	module.Logger().Info("websocket server received: " + string(payload))
	return session.Send(payload)
}

// onClientOpen 在客户端连接建立后发送回环测试消息。
func (module *EchoWebSocketModule) onClientOpen(
	_ context.Context,
	session network.Session,
) error {
	// OnOpen 已在当前 Service 串行上下文执行，可以立即发送第一条消息。
	return session.Send([]byte("hello through websocket"))
}

// onClientMessage 处理服务端回显，并在验证成功后关闭测试连接。
func (module *EchoWebSocketModule) onClientMessage(
	_ context.Context,
	session network.Session,
	payload []byte,
) error {
	module.Logger().Info("websocket client received: " + string(payload))
	// 示例完成后关闭连接；Application 继续运行，按 Ctrl+C 验证 Module 停止。
	session.Close(nil)
	return nil
}

// NetworkService 只提供串行执行边界，并装配项目自己的业务 Module。
type NetworkService struct{ service.Service }

// OnInit 把 WebSocket 业务 Module 接入当前 Service 生命周期。
func (target *NetworkService) OnInit() error {
	return target.AddModule(&EchoWebSocketModule{})
}

// init 只把薄 Service 安装到当前 Application。
func init() { app.Setup(&NetworkService{}) }

// main 启动示例 Application。
func main() { app.Start() }
