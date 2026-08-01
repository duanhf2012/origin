// Package config 提供 Origin 的 JSON/YAML 配置目录加载能力。
package config

import (
	"fmt"
	"reflect"

	"github.com/duanhf2012/origin/v3/errs"
)

// LoadDir 递归加载 dir 中的 JSON/YAML 文件，合并后解码到 dst。
//
// dst 必须是非 nil 指针。加载或解码失败时，dst 保持调用前的值。
func LoadDir(dir string, dst any) error {
	// 在读取文件前验证目标，避免目录错误掩盖调用方 API 使用错误。
	if _, err := validateTarget(dst); err != nil {
		return err
	}
	snapshot, err := LoadSnapshot(dir)
	if err != nil {
		return err
	}
	return snapshot.Decode(dst)
}

// validateTarget 验证公开 dst 参数并返回实际可写元素。
func validateTarget(dst any) (reflect.Value, error) {
	// 无类型 nil 无法通过反射取得可写目标。
	if dst == nil {
		return reflect.Value{}, invalidArgument("配置目标不能为空")
	}
	// LoadDir 需要修改调用方值，因此只接受指针。
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer {
		return reflect.Value{}, invalidArgument("配置目标必须是指针")
	}
	// 有类型 nil 指针同样没有可写 Elem，单独返回明确错误。
	if value.IsNil() {
		return reflect.Value{}, invalidArgument("配置目标不能是 nil 指针")
	}
	// 返回指针指向的实际值，后续解码支持结构体、Map 和其他合法类型。
	return value.Elem(), nil
}

// invalidArgument 创建表示调用方式错误的稳定错误。
func invalidArgument(message string) error {
	// message 只描述公开参数，不包含配置值。
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

// invalidConfig 创建不带精确来源位置的配置错误。
func invalidConfig(message string) error {
	// 目录和解析阶段错误统一使用 CodeInvalidConfig。
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

// invalidConfigAt 创建尽可能包含逻辑文件、行和列的配置错误。
func invalidConfigAt(source sourcePos, format string, args ...any) error {
	// 先格式化业务说明，再按可用来源精度增加前缀。
	message := fmt.Sprintf(format, args...)
	if source.file == "" {
		// 内部模型错误可能没有文件来源，仍返回稳定配置错误码。
		return invalidConfig(message)
	}
	if source.line <= 0 {
		// 文件级错误至少保留逻辑相对路径。
		return invalidConfig(fmt.Sprintf("%s: %s", source.file, message))
	}
	// 节点级错误统一采用 file:line:column 形式，方便编辑器和日志检索。
	return invalidConfig(fmt.Sprintf(
		"%s:%d:%d: %s",
		source.file,
		source.line,
		source.column,
		message,
	))
}
