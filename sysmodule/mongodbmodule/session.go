package mongodbmodule

import (
	"context"

	"github.com/duanhf2012/origin/v3/errs"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

// WithSession 创建官方 Session，把 SessionContext 传给 fn，并在返回前释放 Session。
//
// fn 在调用方 goroutine 中同步执行；Module 不切换到 Service 工作协程。若从外部 goroutine
// 访问仅能在 Service 协程修改的业务状态，应先使用 Module.Await，再在 Await 回调内调用本方法。
func (module *Module) WithSession(
	ctx context.Context,
	fn func(context.Context) error,
	options ...mongooptions.Lister[mongooptions.SessionOptions],
) error {
	if ctx == nil || fn == nil {
		return errs.ErrInvalidArgument
	}
	if err := validateListers(options); err != nil {
		return err
	}
	runtime, err := module.requireRuntime()
	if err != nil {
		return err
	}
	return runtime.withSession(ctx, fn, options...)
}

// WithTransaction 在一个官方事务中同步执行 fn。
//
// Driver 可能根据 TransientTransactionError 或 UnknownTransactionCommitResult 标签重新执行
// fn 或 CommitTransaction。fn 必须幂等，只读写当前事务中的 MongoDB 数据，不能直接发送 RPC、
// Kafka、奖励邮件或修改不可回滚的 Service 内存状态；外部副作用应采用事务后提交或 Outbox。
func (module *Module) WithTransaction(
	ctx context.Context,
	fn func(context.Context) error,
	options ...mongooptions.Lister[mongooptions.TransactionOptions],
) error {
	if ctx == nil || fn == nil {
		return errs.ErrInvalidArgument
	}
	if err := validateListers(options); err != nil {
		return err
	}
	runtime, err := module.requireRuntime()
	if err != nil {
		return err
	}
	return runtime.withTransaction(ctx, fn, options...)
}
