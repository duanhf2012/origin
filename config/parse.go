package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

func parseFile(file configFile) (*valueNode, error) {
	data, err := os.ReadFile(file.path)
	if err != nil {
		return nil, invalidConfig(file.relative + ": 读取配置文件失败: " + err.Error())
	}
	return parseData(file, data)
}

func parseData(file configFile, data []byte) (*valueNode, error) {
	if file.format == ".json" && !json.Valid(data) {
		return nil, invalidConfig(file.relative + ": 文件不是严格 JSON")
	}

	parsed, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, invalidConfig(file.relative + ": " + yaml.FormatError(err, false, false))
	}
	if len(parsed.Docs) != 1 || parsed.Docs[0] == nil || parsed.Docs[0].Body == nil {
		if len(parsed.Docs) > 1 {
			return nil, invalidConfig(file.relative + ": YAML 多文档不受支持")
		}
		return nil, invalidConfig(file.relative + ": 配置文件不能为空")
	}
	body := parsed.Docs[0].Body
	if _, ok := body.(ast.MapNode); !ok {
		return nil, invalidConfigAt(sourceOf(file.relative, body), "配置根节点必须是 Mapping")
	}

	var decoded map[string]any
	if err := yaml.NodeToValue(body, &decoded); err != nil {
		return nil, invalidConfig(file.relative + ": " + yaml.FormatError(err, false, false))
	}
	if decoded == nil {
		decoded = make(map[string]any)
	}

	positions := make(map[string]sourcePos)
	collectPositions(file.relative, body, "", positions)
	rootSource := sourceOf(file.relative, body)
	return newMapping(rootSource, decoded, positions, "")
}

func collectPositions(file string, node ast.Node, path string, positions map[string]sourcePos) {
	if node == nil {
		return
	}
	switch typed := node.(type) {
	case ast.MapNode:
		iterator := typed.MapRange()
		for iterator.Next() {
			keyNode := iterator.Key()
			if keyNode == nil || keyNode.IsMergeKey() {
				// Anchor 合并产生的字段由解码器展开；缺少精确位置时回退到父节点。
				continue
			}
			scalar, ok := keyNode.(ast.ScalarNode)
			if !ok {
				continue
			}
			key, ok := scalar.GetValue().(string)
			if !ok {
				continue
			}
			childPath := joinPath(path, key)
			positions[childPath+"#key"] = sourceOf(file, keyNode)
			value := iterator.Value()
			positions[childPath] = sourceOf(file, value)
			collectPositions(file, value, childPath, positions)
		}
	case *ast.SequenceNode:
		for index, child := range typed.Values {
			childPath := sequencePath(path, index)
			positions[childPath] = sourceOf(file, child)
			collectPositions(file, child, childPath, positions)
		}
	case *ast.SequenceEntryNode:
		collectPositions(file, typed.Value, path, positions)
	case *ast.AnchorNode:
		collectPositions(file, typed.Value, path, positions)
	case *ast.TagNode:
		collectPositions(file, typed.Value, path, positions)
	}
}

func sourceOf(file string, node ast.Node) sourcePos {
	source := sourcePos{file: file}
	if node == nil || node.GetToken() == nil || node.GetToken().Position == nil {
		return source
	}
	source.line = node.GetToken().Position.Line
	source.column = node.GetToken().Position.Column
	return source
}

func sequencePath(parent string, index int) string {
	return parent + "[" + itoa(index) + "]"
}

func itoa(value int) string {
	// 配置数组索引通常很小，使用标准转换保持实现直接。
	return strconv.Itoa(value)
}

func expandEnvironment(node *valueNode) error {
	switch node.kind {
	case kindString:
		value := node.scalar.(string)
		expanded, exact, err := expandString(value, os.LookupEnv)
		if err != nil {
			return invalidConfigAt(node.source, "%s", err.Error())
		}
		node.scalar = expanded
		node.envDerived = exact
	case kindMapping:
		for index := range node.mapping {
			if err := expandEnvironment(node.mapping[index].value); err != nil {
				return err
			}
		}
	case kindSequence:
		for _, child := range node.sequence {
			if err := expandEnvironment(child); err != nil {
				return err
			}
		}
	}
	return nil
}

type environmentLookup func(string) (string, bool)

func expandString(input string, lookup environmentLookup) (string, bool, error) {
	if !strings.Contains(input, "$") {
		return input, false, nil
	}

	var builder strings.Builder
	builder.Grow(len(input))
	placeholderCount := 0
	exact := false

	for index := 0; index < len(input); {
		if input[index] != '$' {
			builder.WriteByte(input[index])
			index++
			continue
		}

		if index+2 < len(input) && input[index+1] == '$' && input[index+2] == '{' {
			end := strings.IndexByte(input[index+3:], '}')
			if end < 0 {
				builder.WriteString(input[index:])
				break
			}
			end += index + 3
			builder.WriteString(input[index+1 : end+1])
			index = end + 1
			continue
		}

		if index+1 >= len(input) || input[index+1] != '{' {
			builder.WriteByte(input[index])
			index++
			continue
		}
		end := strings.IndexByte(input[index+2:], '}')
		if end < 0 {
			return "", false, fmt.Errorf("环境变量占位符缺少右花括号")
		}
		end += index + 2
		name := input[index+2 : end]
		if !validEnvironmentName(name) {
			return "", false, fmt.Errorf("环境变量名称无效: %s", name)
		}
		value, exists := lookup(name)
		if !exists {
			return "", false, fmt.Errorf("环境变量未定义: %s", name)
		}
		builder.WriteString(value)
		placeholderCount++
		exact = index == 0 && end == len(input)-1
		index = end + 1
	}
	return builder.String(), placeholderCount == 1 && exact, nil
}

func validEnvironmentName(name string) bool {
	if name == "" || !isEnvironmentHead(name[0]) {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isEnvironmentHead(character) && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func isEnvironmentHead(character byte) bool {
	return character == '_' ||
		character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}
