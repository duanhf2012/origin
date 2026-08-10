package protocol

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	"github.com/duanhf2012/origin/v3/sysmodule/network"
	"github.com/duanhf2012/origin/v3/sysmodule/network/internal/core"
)

// RouterOptions 配置协议 Codec 和可选的 Session 生命周期回调。
type RouterOptions struct {
	Codec           Codec
	Open            func(context.Context, network.Session) error
	WritableChanged func(context.Context, network.Session, bool)
	Close           func(context.Context, network.Session, error)
	Unknown         func(context.Context, network.Session, MessageID, []byte) error
}

type route struct {
	newValue func() any
	handle   func(context.Context, network.Session, any) error
}

// Router 把协议消息分发给构造期注册的类型 Handler。
type Router struct {
	options RouterOptions

	mu     sync.RWMutex
	routes map[MessageID]route
	frozen bool
}

// NewRouter 创建一个尚可注册、由网络 Module OnInit 自动冻结的 Router。
func NewRouter(options RouterOptions) (*Router, error) {
	if isNilCodec(options.Codec) {
		return nil, invalidConfig("protocol.codec 不能为空")
	}
	return &Router{options: options, routes: make(map[MessageID]route)}, nil
}

// Register 为非零 ID 注册唯一的消息类型和处理函数。
func Register[T any](
	router *Router,
	id MessageID,
	handler func(context.Context, network.Session, *T) error,
) error {
	if router == nil || id == 0 || handler == nil {
		return invalidArgument("protocol: Router、MessageID 和 Handler 必须有效")
	}
	router.mu.Lock()
	defer router.mu.Unlock()
	if router.frozen {
		return invalidConfig("protocol: Router 已冻结，不能继续注册")
	}
	if _, exists := router.routes[id]; exists {
		return invalidConfig(fmt.Sprintf("protocol: MessageID %d 重复注册", id))
	}
	router.routes[id] = route{
		newValue: func() any { return new(T) },
		handle: func(ctx context.Context, session network.Session, value any) error {
			message, ok := value.(*T)
			if !ok || message == nil {
				return protocolError(fmt.Sprintf("protocol: MessageID %d 解码类型不匹配", id))
			}
			return handler(ctx, session, message)
		},
	}
	return nil
}

// Freeze 固定注册表。重复冻结是安全空操作。
func (router *Router) Freeze() error {
	if router == nil {
		return invalidArgument("protocol: Router 不能为空")
	}
	router.mu.Lock()
	router.frozen = true
	router.mu.Unlock()
	return nil
}

// New 实现 Resolver，并只创建已经注册的消息对象。
func (router *Router) New(id MessageID) (any, bool) {
	if router == nil || id == 0 {
		return nil, false
	}
	router.mu.RLock()
	entry, ok := router.routes[id]
	router.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return entry.newValue(), true
}

// Send 直接编码到框架 Buffer，成功后把所有权转移给 Session 发送队列。
func (router *Router) Send(session network.Session, id MessageID, value any) error {
	if router == nil || session == nil || id == 0 || isNilValue(value) {
		return invalidArgument("protocol: Session、MessageID 和消息值必须有效")
	}
	return core.EncodeAndSend(session, func(target *core.Encoder) error {
		return router.options.Codec.Encode(&Encoder{core: target}, id, value)
	})
}

// OnOpen 转发生命周期回调。
func (router *Router) OnOpen(ctx context.Context, session network.Session) error {
	if router.options.Open == nil {
		return nil
	}
	return router.options.Open(ctx, session)
}

// OnMessage 解码并分发一条完整 Raw 消息。
func (router *Router) OnMessage(
	ctx context.Context,
	session network.Session,
	payload []byte,
) error {
	message, err := router.options.Codec.Decode(payload, router)
	if err != nil {
		return err
	}
	if message.ID == 0 {
		return protocolError("protocol: MessageID 不能为零")
	}
	router.mu.RLock()
	entry, exists := router.routes[message.ID]
	router.mu.RUnlock()
	if !exists {
		if router.options.Unknown != nil {
			return router.options.Unknown(ctx, session, message.ID, payload)
		}
		return protocolError(fmt.Sprintf("protocol: 未注册 MessageID %d", message.ID))
	}
	return entry.handle(ctx, session, message.Value)
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// OnWritableChanged 转发可选背压回调。
func (router *Router) OnWritableChanged(
	ctx context.Context,
	session network.Session,
	writable bool,
) {
	if router.options.WritableChanged != nil {
		router.options.WritableChanged(ctx, session, writable)
	}
}

// OnClose 转发最终关闭回调。
func (router *Router) OnClose(ctx context.Context, session network.Session, cause error) {
	if router.options.Close != nil {
		router.options.Close(ctx, session, cause)
	}
}

func isNilCodec(codec Codec) bool {
	return isNilValue(codec)
}

var (
	_ network.Handler = (*Router)(nil)
	_ network.Freezer = (*Router)(nil)
	_ Resolver        = (*Router)(nil)
)
