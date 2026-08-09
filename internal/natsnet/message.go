package natsnet

// Message 是交给 NATS 入站 Handler 的只读消息视图。
//
// Data 直接引用 nats.go 为当前入站消息独立持有的字节。Handler 不能修改，但可以把只读
// Slice 转移给另一个明确所有者并在返回后继续持有；最后一个 Go 引用消失后由 GC 回收。
// natsnet 不池化或复用该底层数组，因此跨 Service 队列转移不需要额外复制。
type Message struct {
	// Subject 是收到消息的实际 NATS Subject。
	Subject string
	// Reply 是发布方可选声明的响应 Subject；为空表示该消息不期待定向回复。
	Reply string
	// Data 是可转移、不可修改的只读 payload。
	Data []byte
}

// MessageHandler 在 nats.go 的异步订阅回调 goroutine 中顺序处理消息。
//
// natsnet 不为每条消息创建 goroutine。Handler 不返回 error；panic 只丢弃当前消息，
// 并通过日志和 EventAsyncError 报告，后续消息仍会继续处理。
type MessageHandler func(message Message)
