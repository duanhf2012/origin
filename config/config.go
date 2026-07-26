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
	target, err := validateTarget(dst)
	if err != nil {
		return err
	}

	files, err := scanDir(dir)
	if err != nil {
		return err
	}

	var root *valueNode
	for _, file := range files {
		current, err := parseFile(file)
		if err != nil {
			return err
		}
		if err := expandEnvironment(current); err != nil {
			return err
		}
		if root == nil {
			root = current
			continue
		}
		if err := mergeNodes(root, current, ""); err != nil {
			return err
		}
	}

	// 先在临时值中完成全部解码，保证任何错误都不会留下半更新结果。
	temporary := reflect.New(target.Type()).Elem()
	temporary.Set(target)
	decoder := valueDecoder{
		fields: make(map[reflect.Type]structFields),
	}
	if err := decoder.decode(temporary, root); err != nil {
		return err
	}
	target.Set(temporary)
	return nil
}

func validateTarget(dst any) (reflect.Value, error) {
	if dst == nil {
		return reflect.Value{}, invalidArgument("配置目标不能为空")
	}
	value := reflect.ValueOf(dst)
	if value.Kind() != reflect.Pointer {
		return reflect.Value{}, invalidArgument("配置目标必须是指针")
	}
	if value.IsNil() {
		return reflect.Value{}, invalidArgument("配置目标不能是 nil 指针")
	}
	return value.Elem(), nil
}

func invalidArgument(message string) error {
	return errs.NewMessage(errs.CodeInvalidArgument, message)
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

func invalidConfigAt(source sourcePos, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if source.file == "" {
		return invalidConfig(message)
	}
	if source.line <= 0 {
		return invalidConfig(fmt.Sprintf("%s: %s", source.file, message))
	}
	return invalidConfig(fmt.Sprintf(
		"%s:%d:%d: %s",
		source.file,
		source.line,
		source.column,
		message,
	))
}
