// 本示例演示用 application.Options.LogHandlerFactory 替换 Origin 的默认日志输出后端。
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/service"
)

// app 在创建时注入 Factory。Origin 仍负责 Logger、队列、调用位置和 Handler 生命周期。
var app = application.New(application.Options{LogHandlerFactory: newJSONHandler})

// CustomHandlerService 演示业务侧仍按普通 Origin Logger 写日志，不感知输出后端的替换。
type CustomHandlerService struct{ service.Service }

// OnStart 记录两条日志，并验证未实现 Controller 的 Handler 仍能正常写日志。
func (target *CustomHandlerService) OnStart(context.Context) error {
	originlog.Info("custom handler is ready", originlog.String("component", "bootstrap"))
	target.Logger().Info("player service is ready", originlog.Int64("player_id", 10001))

	// 本示例的 Handler 没有实现 log.Controller，因此运行时调整输出会返回稳定错误。
	if err := originlog.SetConsoleLevel(originlog.DebugLevel); !errors.Is(err, errs.ErrLogControlUnsupported) {
		return fmt.Errorf("SetConsoleLevel error = %w, want ErrLogControlUnsupported", err)
	}
	target.Logger().Info("runtime control is intentionally unsupported")
	return nil
}

// init 登记配置中的 CustomHandlerService 模板。
func init() { app.Setup(&CustomHandlerService{}) }

// main 把配置加载、日志 Runtime 生命周期和信号处理交给 Application。
func main() { app.Start() }

// newJSONHandler 接收已合并完成的 Origin 日志配置，并返回一个固定输出策略的 Handler。
//
// 本例把 console.level 当作自定义 JSON 输出的最低级别；File、滚动和 context_fields 等内置
// Zap 配置不会自动套用到第三方 Handler，应由项目自己的 Handler 明确实现。
func newJSONHandler(config originlog.Config) (originlog.Handler, error) {
	return &jsonHandler{output: os.Stdout, minimum: config.Console.Level}, nil
}

// jsonHandler 是一个最小但完整的 JSON Lines Handler。它不实现 log.Controller，说明运行时
// Console/File 控制是可选扩展，而不是替换输出后端的前置条件。
type jsonHandler struct {
	output  io.Writer
	minimum originlog.Level
}

// Enabled 可被业务协程并发调用；本例的 minimum 在构造后不再改变，因此无需额外锁。
func (handler *jsonHandler) Enabled(level originlog.Level) bool {
	return level >= handler.minimum
}

// Write 把一次 Origin 日志调用同步编码为单行 JSON。
// Runtime 保证 Write 与 Sync、Close 串行调用；fields 仅在本次调用有效，不能保存到方法外。
func (handler *jsonHandler) Write(record originlog.Record, fields []originlog.Field) error {
	document := map[string]any{
		"time":    record.Time.UTC().Format(time.RFC3339Nano),
		"level":   record.Level.String(),
		"caller":  fmt.Sprintf("%s:%d", record.Caller.File, record.Caller.Line),
		"message": record.Message,
	}
	if record.Stack != "" {
		document["stack"] = record.Stack
	}
	if len(fields) != 0 {
		values := make(map[string]any, len(fields))
		for _, field := range fields {
			values[field.Key()] = fieldValue(field)
		}
		document["fields"] = values
	}
	return json.NewEncoder(handler.output).Encode(document)
}

// Sync 是 Runtime 在关闭前调用的刷新钩子。os.Stdout 没有额外缓冲，因此本例无需动作。
func (*jsonHandler) Sync() error { return nil }

// Close 是 Runtime 最后一次调用的资源释放钩子。本例不关闭进程拥有的 os.Stdout。
func (*jsonHandler) Close() error { return nil }

// fieldValue 把 Origin Field 转成标准 JSON 能编码的值；真实项目可改为映射到自己的日志库字段。
func fieldValue(field originlog.Field) any {
	switch field.Kind() {
	case originlog.StringField, originlog.ErrorField:
		return field.StringValue()
	case originlog.BoolField:
		return field.BoolValue()
	case originlog.IntField, originlog.Int32Field, originlog.Int64Field:
		return field.Int64Value()
	case originlog.UintField, originlog.Uint32Field, originlog.Uint64Field:
		return field.Uint64Value()
	case originlog.Float32Field:
		return field.Float32Value()
	case originlog.Float64Field:
		return field.Float64Value()
	case originlog.DurationField:
		return field.DurationValue().String()
	case originlog.TimeField:
		return field.TimeValue().UTC().Format(time.RFC3339Nano)
	case originlog.BytesField:
		// 任意字节不一定是合法 UTF-8；Base64 可避免 JSON 编码时替换无效字节而丢失数据。
		return base64.StdEncoding.EncodeToString(field.BytesValue())
	case originlog.AnyField:
		// AnyField 已在调用点序列化为 JSON 快照；复制后交给 Encoder，避免保留 Runtime 所有切片。
		return json.RawMessage(append([]byte(nil), field.BytesValue()...))
	default:
		return nil
	}
}
