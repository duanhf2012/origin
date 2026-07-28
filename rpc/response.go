package rpc

import (
	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/internal/bufferpool"
)

// ResponseWriter 允许生成 Dispatcher 在业务方法返回后申请一次准确大小的响应 Buffer。
//
// Runtime 在目标任务栈上创建该值。生成代码只能调用一次 Allocate；Buffer 指针保持未导出，
// Dispatcher 返回后由 Runtime 取得唯一所有权并负责交给调用方或释放。
type ResponseWriter struct {
	pool    *bufferpool.Pool
	maxSize int
	buffer  *bufferpool.Buffer
}

// newResponseWriter 创建只属于一次 CallRequest 的栈上响应写入器。
func newResponseWriter(pool *bufferpool.Pool, maxSize int) ResponseWriter {
	return ResponseWriter{
		pool:    pool,
		maxSize: maxSize,
	}
}

// Allocate 取得准确大小的最终响应可写区域。
//
// 重复调用、无效长度、缺少 Runtime Pool 或超过消息上限都返回稳定编码错误，且不会替换
// 已经取得的 Buffer。
func (writer *ResponseWriter) Allocate(size int) ([]byte, error) {
	if writer == nil ||
		writer.pool == nil ||
		writer.buffer != nil ||
		size < 0 ||
		size > writer.maxSize {
		return nil, errs.ErrRPCEncodeFailed
	}
	writer.buffer = writer.pool.Acquire(size)
	return writer.buffer.Bytes(), nil
}

// take 把 Dispatcher 成功生成的 Buffer 唯一所有权移交给 Runtime。
func (writer *ResponseWriter) take() *bufferpool.Buffer {
	if writer == nil {
		return nil
	}
	buffer := writer.buffer
	writer.buffer = nil
	return buffer
}

// release 在 Dispatcher 失败或 panic 时归还尚未移交的响应 Buffer。
func (writer *ResponseWriter) release() {
	if writer == nil || writer.buffer == nil {
		return
	}
	writer.buffer.Release()
	writer.buffer = nil
}
