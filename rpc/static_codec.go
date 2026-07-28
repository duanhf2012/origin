package rpc

// StaticCodec 描述 origingen 可以在生成期选择的无状态自定义静态 Codec。
//
// T 是一个精确具名目标类型。实现者必须使用带 //origin:rpc-codec 标记的已导出空结构体，
// 并以值接收者实现三个方法。该接口只用于公开说明和可选的编译期断言；生成代码始终直接
// 调用具体 Provider，不把它转换成接口，因此 RPC 热路径不会产生接口装箱或动态分派。
type StaticCodec[T any] interface {
	// Size 返回 value 的自定义 payload 准确长度。
	//
	// value 始终非 nil。返回长度可以为零，但不能超过 RPC 单消息上限；实现不得修改 value。
	Size(value *T) (int, error)

	// MarshalTo 把 value 直接写入 Origin 提供的最终 payload 区域。
	//
	// dst 的长度严格等于 Size 的返回值。实现不得保存、扩容或释放 dst，也不得把它交给
	// 其他 goroutine；返回的写入长度必须严格等于 len(dst)。
	MarshalTo(dst []byte, value *T) (int, error)

	// Unmarshal 从当前值独占的只读 payload 中恢复 value。
	//
	// 实现不得保存 src，也不得让 value 中的业务可见 Slice、Map、string 或指针继续引用
	// src；需要保存数据时必须建立业务独立所有权。
	Unmarshal(src []byte, value *T) error
}
