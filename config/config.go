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
	target, err := validateTarget(dst)
	if err != nil {
		return err
	}

	// 一次性发现并稳定排序全部候选文件，后续合并顺序只依赖相对路径。
	files, err := scanDir(dir)
	if err != nil {
		return err
	}

	// root 只在本次调用内存在，逐文件解析、展开并合并来源树。
	var root *valueNode
	for _, file := range files {
		// 每个文件按扩展名严格解析，同时保留逻辑相对路径和行列。
		current, err := parseFile(file)
		if err != nil {
			return err
		}
		// 环境变量必须在单文件语法解析后、跨文件合并前展开字符串值。
		if err := expandEnvironment(current); err != nil {
			return err
		}
		// 第一个根节点直接成为合并目标，避免一次无意义深拷贝。
		if root == nil {
			root = current
			continue
		}
		// 后续根 Mapping 按确定规则递归合并，重复 Scalar 立即失败。
		if err := mergeNodes(root, current, ""); err != nil {
			return err
		}
	}

	// 先在临时值中完成全部解码，保证任何错误都不会留下半更新结果。
	temporary := reflect.New(target.Type()).Elem()
	// 浅复制调用方默认值；解码器会在修改 Pointer、Map、Slice 前建立独立对象。
	temporary.Set(target)
	// 字段缓存仅属于本次加载，避免包级反射状态污染多个 Application。
	decoder := valueDecoder{
		fields: make(map[reflect.Type]structFields),
	}
	// 严格解码完整合并树，未知字段和类型错误均带来源位置返回。
	if err := decoder.decode(temporary, root); err != nil {
		return err
	}
	// 只有全部步骤成功后才原子式提交到调用方目标。
	target.Set(temporary)
	return nil
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
