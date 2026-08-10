// 本示例展示同一个 Service 托管 TCP Server 和 Client，并通过回环地址调用自己。
package main

import (
	"context"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/tcp"
)

var app = application.New()

// NetworkService 是 Server 与 Client 的共同 Service 串行执行边界。
type NetworkService struct {
	service.Service
	server *tcp.Server
	client *tcp.Client
}

// OnInit 构造网络 Module 并按“先 Server、后 Client”的顺序加入当前 Service。
func (target *NetworkService) OnInit() error {
	serverHandler := network.HandlerFuncs{
		Message: func(
			_ context.Context,
			session network.Session,
			payload []byte,
		) error {
			// payload 只在当前回调返回前有效；Send 会安全复制，所以可以直接回显。
			target.Logger().Info("server received: " + string(payload))
			return session.Send(payload)
		},
	}
	server, err := tcp.NewServer(
		"127.0.0.1:19090",
		tcp.DefaultServerOptions(serverHandler),
	)
	if err != nil {
		return err
	}

	clientHandler := network.HandlerFuncs{
		Open: func(_ context.Context, session network.Session) error {
			// OnOpen 已在当前 Service 串行上下文执行，可以立即发送第一条消息。
			return session.Send([]byte("hello from the same service"))
		},
		Message: func(
			_ context.Context,
			session network.Session,
			payload []byte,
		) error {
			target.Logger().Info("client received: " + string(payload))
			// 示例完成后关闭连接；Application 继续运行，按 Ctrl+C 验证 Module 停止。
			session.Close(nil)
			return nil
		},
	}
	client, err := tcp.NewClient(
		"127.0.0.1:19090",
		tcp.DefaultClientOptions(clientHandler),
	)
	if err != nil {
		return err
	}

	target.server = server
	target.client = client
	if err := target.AddModule(server); err != nil {
		return err
	}
	return target.AddModule(client)
}

func init() { app.Setup(&NetworkService{}) }

func main() { app.Start() }
