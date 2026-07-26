package config

import (
	"fmt"
	"sort"
)

type valueKind uint8

const (
	kindInvalid valueKind = iota
	kindNull
	kindBool
	kindInteger
	kindUnsigned
	kindFloat
	kindString
	kindMapping
	kindSequence
)

type sourcePos struct {
	file   string
	line   int
	column int
}

type mappingEntry struct {
	key       string
	keySource sourcePos
	value     *valueNode
}

// valueNode 是一次 LoadDir 调用内部使用的配置值。
// 它保留来源但不保存原始文件内容，既便于定位错误，也避免错误意外泄露配置。
type valueNode struct {
	kind       valueKind
	source     sourcePos
	scalar     any
	envDerived bool
	mapping    []mappingEntry
	sequence   []*valueNode
}

func newMapping(source sourcePos, values map[string]any, positions map[string]sourcePos, path string) (*valueNode, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	node := &valueNode{
		kind:    kindMapping,
		source:  source,
		mapping: make([]mappingEntry, 0, len(keys)),
	}
	for _, key := range keys {
		childPath := joinPath(path, key)
		childSource := positions[childPath]
		if childSource.file == "" {
			childSource = source
		}
		child, err := newValueNode(values[key], childSource, positions, childPath)
		if err != nil {
			return nil, err
		}
		keySource := positions[childPath+"#key"]
		if keySource.file == "" {
			keySource = childSource
		}
		node.mapping = append(node.mapping, mappingEntry{
			key:       key,
			keySource: keySource,
			value:     child,
		})
	}
	return node, nil
}

func newValueNode(value any, source sourcePos, positions map[string]sourcePos, path string) (*valueNode, error) {
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
		return newMapping(source, typed, positions, path)
	case []any:
		sequence := &valueNode{
			kind:     kindSequence,
			source:   source,
			sequence: make([]*valueNode, 0, len(typed)),
		}
		for index, item := range typed {
			childPath := fmt.Sprintf("%s[%d]", path, index)
			childSource := positions[childPath]
			if childSource.file == "" {
				childSource = source
			}
			child, err := newValueNode(item, childSource, positions, childPath)
			if err != nil {
				return nil, err
			}
			sequence.sequence = append(sequence.sequence, child)
		}
		return sequence, nil
	default:
		return nil, invalidConfigAt(source, "不支持的配置值类型 %T", value)
	}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func (node *valueNode) kindName() string {
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
		return "Invalid"
	}
}
