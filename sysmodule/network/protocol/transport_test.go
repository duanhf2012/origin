package protocol_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/service"
	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/protocol"
	protocoljson "github.com/duanhf2012/origin/v3/sysmodule/network/protocol/json"
	protocolpb "github.com/duanhf2012/origin/v3/sysmodule/network/protocol/pb"
	"github.com/duanhf2012/origin/v3/sysmodule/network/tcp"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type transportService struct {
	service.Service
	server *tcp.Server
	client *tcp.Client
}

// customCodec 演示自定义协议只依赖 Encoder，不接触 Pool 或 Session 内部所有权。
type customCodec struct{}

func (customCodec) Decode(raw []byte, resolver protocol.Resolver) (protocol.Message, error) {
	if len(raw) == 0 {
		return protocol.Message{}, errs.ErrTransportProtocol
	}
	id := protocol.MessageID(raw[0])
	value, exists := resolver.New(id)
	if !exists {
		return protocol.Message{ID: id}, nil
	}
	target, ok := value.(*string)
	if !ok {
		return protocol.Message{}, errs.ErrTransportProtocol
	}
	*target = string(raw[1:])
	return protocol.Message{ID: id, Value: target}, nil
}

func (customCodec) Encode(encoder *protocol.Encoder, id protocol.MessageID, value any) error {
	text, ok := value.(string)
	if !ok || id == 0 || id > 255 {
		return errs.ErrInvalidArgument
	}
	if err := encoder.AppendByte(byte(id)); err != nil {
		return err
	}
	region, err := encoder.Reserve(len(text))
	if err != nil {
		return err
	}
	copy(region, text)
	if encoder.Len() != 1+len(text) {
		return errs.ErrInternal
	}
	return nil
}

func (target *transportService) OnInit() error {
	if err := target.AddModule(target.server); err != nil {
		return err
	}
	return target.AddModule(target.client)
}

func TestRouterSendWireGolden(t *testing.T) {
	pbCodec, err := protocolpb.NewCodec(network.LittleEndian)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		codec protocol.Codec
		id    protocol.MessageID
		value any
		want  []byte
	}{
		{
			name:  "json",
			codec: protocoljson.NewCodec(),
			id:    17,
			value: struct {
				Text string `json:"text"`
			}{Text: "hello"},
			want: []byte(`{"id":17,"data":{"text":"hello"}}`),
		},
		{
			name:  "pb little endian",
			codec: pbCodec,
			id:    0x1234,
			value: wrapperspb.String("ok"),
			want:  []byte{0x34, 0x12, 0x0a, 0x02, 'o', 'k'},
		},
		{
			name:  "custom codec",
			codec: customCodec{},
			id:    23,
			value: "ok",
			want:  []byte{23, 'o', 'k'},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := make(chan []byte, 1)
			serverHandler := network.HandlerFuncs{Message: func(
				_ context.Context,
				_ network.Session,
				payload []byte,
			) error {
				raw <- append([]byte(nil), payload...)
				return nil
			}}
			var router *protocol.Router
			router, err = protocol.NewRouter(protocol.RouterOptions{
				Codec: test.codec,
				Open: func(_ context.Context, session network.Session) error {
					return router.Send(session, test.id, test.value)
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			address := reserveAddress(t)
			server, err := tcp.NewServer(address, tcp.DefaultServerOptions(serverHandler))
			if err != nil {
				t.Fatal(err)
			}
			client, err := tcp.NewClient(address, tcp.DefaultClientOptions(router))
			if err != nil {
				t.Fatal(err)
			}
			owner := &transportService{server: server, client: client}
			current := newNode(t, owner)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := current.Start(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-raw:
				if string(got) != string(test.want) {
					t.Fatalf("wire=%x want=%x", got, test.want)
				}
			case <-ctx.Done():
				t.Fatal("等待协议消息超时")
			}
			if err := current.Stop(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCustomCodecDecodeThroughRouter(t *testing.T) {
	router, err := protocol.NewRouter(protocol.RouterOptions{Codec: customCodec{}})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if err := protocol.Register(router, 23, func(
		_ context.Context,
		_ network.Session,
		value *string,
	) error {
		called = *value == "ok"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := router.OnMessage(t.Context(), nil, []byte{23, 'o', 'k'}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("自定义 Codec 未把消息交给类型 Handler")
	}
}

func reserveAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func newNode(t *testing.T, target service.IService) *node.Node {
	t.Helper()
	current, err := node.New(
		node.Config{ID: "protocol-wire", Services: []string{"Transport"}},
		[]node.ServiceBinding{{Name: "Transport", Template: "Transport", Service: target}},
		originlog.NewNop(),
		node.Options{MaxTimersPerNode: 64, TimerLocation: time.UTC},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = current.Rollback(context.Background()) })
	return current
}
