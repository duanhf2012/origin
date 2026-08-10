package network

import "context"

// Handler 接收一条 Session 的有序生命周期和 Raw 逻辑消息。
//
// 同一 Session 严格执行 Open、零到多次 Message/WritableChanged、Close；全部回调进入所属
// Service 串行上下文。OnMessage 的 payload 只在同步回调返回前有效，长期保存必须复制。
type Handler interface {
	// OnOpen 在读取第一条业务消息前执行；返回错误关闭当前 Session。
	OnOpen(ctx context.Context, session Session) error
	// OnMessage 处理一条完整逻辑消息；返回错误关闭当前 Session。
	OnMessage(ctx context.Context, session Session, payload []byte) error
	// OnWritableChanged 只在发送队列跨越高/低水位时执行。
	OnWritableChanged(ctx context.Context, session Session, writable bool)
	// OnClose 在当前 Session 的全部其他事件之后恰好执行一次。
	OnClose(ctx context.Context, session Session, cause error)
}

// HandlerFuncs 用可选函数字段实现 Handler，适合简单 Raw 场景。
//
// nil 字段使用安全空操作：Open/Message 成功，WritableChanged/Close 不执行额外逻辑。
type HandlerFuncs struct {
	// Open 对应 Handler.OnOpen。
	Open func(context.Context, Session) error
	// Message 对应 Handler.OnMessage。
	Message func(context.Context, Session, []byte) error
	// WritableChanged 对应 Handler.OnWritableChanged。
	WritableChanged func(context.Context, Session, bool)
	// Close 对应 Handler.OnClose。
	Close func(context.Context, Session, error)
}

// OnOpen 调用可选 Open 函数。
func (handler HandlerFuncs) OnOpen(ctx context.Context, session Session) error {
	if handler.Open == nil {
		return nil
	}
	return handler.Open(ctx, session)
}

// OnMessage 调用可选 Message 函数。
func (handler HandlerFuncs) OnMessage(
	ctx context.Context,
	session Session,
	payload []byte,
) error {
	if handler.Message == nil {
		return nil
	}
	return handler.Message(ctx, session, payload)
}

// OnWritableChanged 调用可选 WritableChanged 函数。
func (handler HandlerFuncs) OnWritableChanged(
	ctx context.Context,
	session Session,
	writable bool,
) {
	if handler.WritableChanged != nil {
		handler.WritableChanged(ctx, session, writable)
	}
}

// OnClose 调用可选 Close 函数。
func (handler HandlerFuncs) OnClose(ctx context.Context, session Session, cause error) {
	if handler.Close != nil {
		handler.Close(ctx, session, cause)
	}
}

// Freezer 是网络 Module 在 OnInit 识别并冻结构造期注册表的可选契约。
//
// 普通业务 Handler 不需要实现；协议 Router 实现该接口以拒绝运行期注册。
type Freezer interface {
	Freeze() error
}
