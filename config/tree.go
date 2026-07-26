package config

import (
	"fmt"
	"sort"
)

// valueKind 是与 JSON/YAML 共同数据模型对应的内部节点类型。
type valueKind uint8

const (
	// kindInvalid 是未初始化占位，不应进入解码。
	kindInvalid valueKind = iota
	// Scalar 类型分开保存，避免后续依赖 interface{} 猜测数值。
	kindNull
	kindBool
	kindInteger
	kindUnsigned
	kindFloat
	kindString
	kindMapping
	kindSequence
)

// sourcePos 表示逻辑配置文件中的一个起始位置。
type sourcePos struct {
	// file 是相对配置根目录的逻辑斜杠路径。
	file string
	// line 和 column 使用解析器返回的 1 起始位置；零表示不可精确定位。
	line   int
	column int
}

// mappingEntry 同时保存 Key 位置和值节点位置。
type mappingEntry struct {
	// key 是经过解析且不做环境变量展开的字符串字段名。
	key string
	// keySource 用于未知字段错误，value 保存字段值及其独立来源。
	keySource sourcePos
	value     *valueNode
}

// valueNode 是一次 LoadDir 调用内部使用的配置值。
// 它保留来源但不保存原始文件内容，既便于定位错误，也避免错误意外泄露配置。
type valueNode struct {
	// kind 决定 scalar、mapping、sequence 三组存储中哪一组有效。
	kind valueKind
	// source 是当前值节点的最佳可用来源位置。
	source sourcePos
	// scalar 保存规范化的 bool/int64/uint64/float64/string 值。
	scalar any
	// envDerived 只表示该字符串原来恰好由一个环境变量占位符组成。
	envDerived bool
	// mapping 和 sequence 保持解析及排序后稳定顺序。
	mapping  []mappingEntry
	sequence []*valueNode
}

// newMapping 把通用 Map 转为稳定排序且带来源的内部 Mapping。
func newMapping(source sourcePos, values map[string]any, positions map[string]sourcePos, path string) (*valueNode, error) {
	// Go Map 枚举顺序不稳定，先复制并排序全部 Key。
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// 按排序后容量建立 Mapping，后续合并会保持该稳定顺序。
	node := &valueNode{
		kind:    kindMapping,
		source:  source,
		mapping: make([]mappingEntry, 0, len(keys)),
	}
	for _, key := range keys {
		// 使用统一路径查找 Value 的精确来源，缺失时回退到父节点。
		childPath := joinPath(path, key)
		childSource := positions[childPath]
		if childSource.file == "" {
			childSource = source
		}
		// 递归把解析器通用值转换为类型明确的内部节点。
		child, err := newValueNode(values[key], childSource, positions, childPath)
		if err != nil {
			return nil, err
		}
		// Key 来源单独保存，用于未知字段错误指向字段名而非字段值。
		keySource := positions[childPath+"#key"]
		if keySource.file == "" {
			keySource = childSource
		}
		// 当前条目建立完成后才追加，错误路径不会留下部分可见结果。
		node.mapping = append(node.mapping, mappingEntry{
			key:       key,
			keySource: keySource,
			value:     child,
		})
	}
	return node, nil
}

// newValueNode 把解析器返回的共同数据模型递归规范为内部节点。
func newValueNode(value any, source sourcePos, positions map[string]sourcePos, path string) (*valueNode, error) {
	// 解析器可能返回不同位宽的基础数值，统一规范到 int64/uint64/float64。
	switch typed := value.(type) {
	case nil:
		return &valueNode{kind: kindNull, source: source}, nil
	case bool:
		return &valueNode{kind: kindBool, source: source, scalar: typed}, nil
	case int:
		return &valueNode{kind: kindInteger, source: source, scalar: int64(typed)}, nil
	case int8:
		return &valueNode{kind: kindInteger, source: source, scalar: int64(typed)}, nil
	case int16:
		return &valueNode{kind: kindInteger, source: source, scalar: int64(typed)}, nil
	case int32:
		return &valueNode{kind: kindInteger, source: source, scalar: int64(typed)}, nil
	case int64:
		return &valueNode{kind: kindInteger, source: source, scalar: typed}, nil
	case uint:
		return &valueNode{kind: kindUnsigned, source: source, scalar: uint64(typed)}, nil
	case uint8:
		return &valueNode{kind: kindUnsigned, source: source, scalar: uint64(typed)}, nil
	case uint16:
		return &valueNode{kind: kindUnsigned, source: source, scalar: uint64(typed)}, nil
	case uint32:
		return &valueNode{kind: kindUnsigned, source: source, scalar: uint64(typed)}, nil
	case uint64:
		return &valueNode{kind: kindUnsigned, source: source, scalar: typed}, nil
	case float32:
		return &valueNode{kind: kindFloat, source: source, scalar: float64(typed)}, nil
	case float64:
		return &valueNode{kind: kindFloat, source: source, scalar: typed}, nil
	case string:
		return &valueNode{kind: kindString, source: source, scalar: typed}, nil
	case map[string]any:
		// Mapping 继续执行 Key 排序和来源绑定。
		return newMapping(source, typed, positions, path)
	case []any:
		// Sequence 预分配准确长度并保持原始元素顺序。
		sequence := &valueNode{
			kind:     kindSequence,
			source:   source,
			sequence: make([]*valueNode, 0, len(typed)),
		}
		for index, item := range typed {
			// 元素路径带索引；缺失精确位置时回退到 Sequence 来源。
			childPath := fmt.Sprintf("%s[%d]", path, index)
			childSource := positions[childPath]
			if childSource.file == "" {
				childSource = source
			}
			// 递归转换完成后再追加，确保节点类型始终有效。
			child, err := newValueNode(item, childSource, positions, childPath)
			if err != nil {
				return nil, err
			}
			sequence.sequence = append(sequence.sequence, child)
		}
		return sequence, nil
	default:
		// 非共同数据模型类型不能进入合并和反射解码阶段。
		return nil, invalidConfigAt(source, "不支持的配置值类型 %T", value)
	}
}

// joinPath 连接内部 Mapping 路径。
func joinPath(parent, key string) string {
	// 顶层不加前导点，嵌套层级使用点号形成稳定诊断路径。
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// kindName 返回面向错误消息的稳定节点类型名称。
func (node *valueNode) kindName() string {
	// Integer 和 Unsigned 对使用者都显示为 Integer，隐藏解析器内部差异。
	switch node.kind {
	case kindNull:
		return "Null"
	case kindBool:
		return "Bool"
	case kindInteger, kindUnsigned:
		return "Integer"
	case kindFloat:
		return "Float"
	case kindString:
		return "String"
	case kindMapping:
		return "Mapping"
	case kindSequence:
		return "Sequence"
	default:
		// 未初始化或损坏节点显式显示 Invalid。
		return "Invalid"
	}
}
