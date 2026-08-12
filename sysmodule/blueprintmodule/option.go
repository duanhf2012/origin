package blueprintmodule

import (
	"reflect"

	blueprint "github.com/duanhf2012/OriginBlueprint/engine/go/blueprint"
)

// Option 是 Blueprint Module 的封闭构造选项。
type Option interface{ apply(*moduleOptions) error }

type optionFunc func(*moduleOptions) error

func (fn optionFunc) apply(options *moduleOptions) error { return fn(options) }

type moduleOptions struct {
	traceLogger    blueprint.BlueprintTraceLogger
	diagnosticSink blueprint.BlueprintDiagnosticSink
}

// WithTraceLogger 安装并发安全的逐节点 Trace Logger。
//
// 安装 Logger 不会自动开启 Trace；使用者只应在短期诊断窗口调用 SetTraceEnabled。Logger 可能从取消或
// 恢复路径并发调用，不能直接访问 Service 串行业务字段。
func WithTraceLogger(logger BlueprintTraceLogger) Option {
	return optionFunc(func(options *moduleOptions) error {
		if isNilInterface(logger) {
			return invalidConfig("blueprintmodule Trace Logger 不能为空")
		}
		if options.traceLogger != nil {
			return invalidConfig("blueprintmodule Trace Logger 只能设置一次")
		}
		options.traceLogger = logger
		return nil
	})
}

// WithDiagnosticSink 安装并发安全的蓝图终态失败接收器。
//
// Sink 只应用于日志和监控上报；需要修改业务状态时使用 Run 返回值或 Execution.OnComplete。
func WithDiagnosticSink(sink BlueprintDiagnosticSink) Option {
	return optionFunc(func(options *moduleOptions) error {
		if isNilInterface(sink) {
			return invalidConfig("blueprintmodule Diagnostic Sink 不能为空")
		}
		if options.diagnosticSink != nil {
			return invalidConfig("blueprintmodule Diagnostic Sink 只能设置一次")
		}
		options.diagnosticSink = sink
		return nil
	})
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	current := reflect.ValueOf(value)
	switch current.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return current.IsNil()
	default:
		return false
	}
}
