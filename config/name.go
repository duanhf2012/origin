package config

import (
	"reflect"
	"strconv"
	"strings"
)

// structField 保存一个配置名到嵌套结构体字段索引路径的映射。
type structField struct {
	// name 是 json Tag 或 Go 字段原名得到的精确配置名。
	name string
	// index 是从根结构体开始的 reflect.FieldByIndex 等价路径。
	index []int
}

// structFields 是某个目标结构体在单次 LoadDir 中的字段模型缓存。
type structFields struct {
	// byName 用于严格 Key 查找；err 保存该 Go 模型自身的冲突。
	byName map[string]structField
	err    error
}

// collectStructFields 建立结构体可导出字段的严格配置名索引。
func collectStructFields(target reflect.Type) structFields {
	// 每次收集使用独立 Map，并把递归或名称冲突保存为模型错误。
	result := structFields{
		byName: make(map[string]structField),
	}
	// root 用于稳定错误文本，visiting 用于检测递归匿名嵌入。
	collectFields(target, target, nil, result.byName, make(map[reflect.Type]bool), &result.err)
	return result
}

// collectFields 深度优先展开匿名结构体并登记可导出字段。
func collectFields(
	root reflect.Type,
	target reflect.Type,
	parentIndex []int,
	fields map[string]structField,
	visiting map[reflect.Type]bool,
	resultErr *error,
) {
	// 之前分支已经发现模型错误时立即停止，避免错误被后续字段覆盖。
	if *resultErr != nil {
		return
	}
	// 当前类型仍在递归栈中说明匿名指针或结构形成了环。
	if visiting[target] {
		*resultErr = invalidConfig("配置结构体 " + root.String() + " 存在递归匿名嵌入")
		return
	}
	// 只在当前 DFS 分支标记；兄弟字段重复嵌入仍由名称冲突规则判断。
	visiting[target] = true
	defer delete(visiting, target)

	// 按 Go 声明顺序遍历字段，错误索引保持可复现。
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		// PkgPath 非空表示未导出字段，反射不能也不应写入。
		if field.PkgPath != "" {
			continue
		}

		// 只读取 json Tag 的名称部分；"-" 直接排除整个字段。
		tagName, ignored := jsonTagName(field.Tag.Get("json"))
		if ignored {
			continue
		}
		// 为当前字段创建独立索引切片，避免递归 append 覆盖父路径。
		fieldIndex := appendIndex(parentIndex, index)

		// 匿名指针按其元素类型判断是否展开。
		embeddedType := field.Type
		if embeddedType.Kind() == reflect.Pointer {
			embeddedType = embeddedType.Elem()
		}
		// 未显式命名的匿名结构体默认扁平展开。
		if field.Anonymous && tagName == "" && embeddedType.Kind() == reflect.Struct {
			collectFields(root, embeddedType, fieldIndex, fields, visiting, resultErr)
			continue
		}

		// Tag 名为空时使用 Go 字段原名，避免隐式改变公开字段名称。
		name := tagName
		if name == "" {
			name = field.Name
		}
		// 同一个配置名只能对应一个字段，禁止依赖声明顺序静默选择。
		if previous, exists := fields[name]; exists {
			*resultErr = invalidConfig(
				"配置结构体 " + root.String() + " 的字段 " +
					field.Name + " 与索引 " + formatFieldIndex(previous.index) +
					" 映射为相同名称 " + name,
			)
			return
		}
		// 字段模型合法后登记完整索引路径。
		fields[name] = structField{name: name, index: fieldIndex}
	}
}

// appendIndex 复制父索引路径并追加当前字段索引。
func appendIndex(parent []int, index int) []int {
	// 新建切片而不是直接 append parent，避免共享底层数组破坏兄弟路径。
	result := make([]int, len(parent)+1)
	copy(result, parent)
	result[len(parent)] = index
	return result
}

// jsonTagName 只解析标准 json Tag 的名称和忽略标记。
func jsonTagName(tag string) (name string, ignored bool) {
	// 没有 Tag 时由调用方使用 Go 字段原名。
	if tag == "" {
		return "", false
	}
	// 逗号后的 omitempty 等编码选项不参与配置解码。
	name, _, _ = strings.Cut(tag, ",")
	if name == "-" {
		// "-" 是唯一忽略语法。
		return "", true
	}
	return name, false
}

// formatFieldIndex 把反射索引路径转换为错误消息使用的点分十进制文本。
func formatFieldIndex(index []int) string {
	// Builder 避免在循环中重复字符串拼接。
	var builder strings.Builder
	for position, value := range index {
		// 从第二段开始插入点号。
		if position > 0 {
			builder.WriteByte('.')
		}
		builder.WriteString(strconv.Itoa(value))
	}
	// 结果仅用于配置模型冲突诊断。
	return builder.String()
}
