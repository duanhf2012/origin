package natsnet

// Message 是交给 NATS 入站 Handler 的只读消息视图。
//
// Data 直接引用 nats.go 为当前入站消息分配的字节。Handler 可以同步读取，但不能修改；
// 需要跨 goroutine 或在 Handler 返回后保留时，新的所有者必须自行复制。
type Message struct {
	// Subject 是收到消息的实际 NATS Subject。
	Subject string
	// Data 是只在当前 Handler 所有权窗口内有效的只读 payload。
	Data []byte
}

// MessageHandler 在 nats.go 的异步订阅回调 goroutine 中顺序处理消息。
//
// natsnet 不为每条消息创建 goroutine。Handler 不返回 error；panic 只丢弃当前消息，
// 并通过日志和 EventAsyncError 报告，后续消息仍会继续处理。
type MessageHandler func(message Message)
