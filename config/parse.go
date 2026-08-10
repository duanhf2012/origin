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

// parseFile 读取单个配置文件并交给不依赖文件系统的解析阶段。
func parseFile(file configFile) (*valueNode, error) {
	// 文件只读取一次；内部树不长期保留原始字节。
	data, err := os.ReadFile(file.path)
	if err != nil {
		return nil, invalidConfig(file.relative + ": 读取配置文件失败: " + err.Error())
	}
	// parseData 使用逻辑相对路径生成稳定错误来源。
	return parseData(file, data)
}

// parseData 按扩展名解析一个文档并建立带来源位置的内部树。
func parseData(file configFile, data []byte) (*valueNode, error) {
	// JSON 先通过标准库严格验证，明确拒绝 YAML、注释和尾随逗号。
	if file.format == ".json" && !json.Valid(data) {
		return nil, invalidConfig(file.relative + ": 文件不是严格 JSON")
	}

	// goccy Parser 同时提供 YAML/JSON AST 及 Token 行列信息。
	parsed, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, invalidConfig(file.relative + ": " + yaml.FormatError(err, false, false))
	}
	// 每个配置文件只接受一个非空文档，避免隐式文档合并语义。
	if len(parsed.Docs) != 1 || parsed.Docs[0] == nil || parsed.Docs[0].Body == nil {
		if len(parsed.Docs) > 1 {
			return nil, invalidConfig(file.relative + ": YAML 多文档不受支持")
		}
		return nil, invalidConfig(file.relative + ": 配置文件不能为空")
	}
	// 完整配置片段必须以 Mapping 为根，才能执行确定性跨文件合并。
	body := parsed.Docs[0].Body
	if _, ok := body.(ast.MapNode); !ok {
		return nil, invalidConfigAt(sourceOf(file.relative, body), "配置根节点必须是 Mapping")
	}

	// AST 转值只做语法层解码；目标结构体规则由后续自有解码器统一处理。
	var decoded map[string]any
	if err := yaml.NodeToValue(body, &decoded); err != nil {
		return nil, invalidConfig(file.relative + ": " + yaml.FormatError(err, false, false))
	}
	if decoded == nil {
		// 显式空 Mapping 规范为可合并的空 Map。
		decoded = make(map[string]any)
	}

	// 先沿 AST 收集路径到行列的索引，再把值转换成轻量内部节点。
	positions := make(map[string]sourcePos)
	collectPositions(file.relative, body, "", positions)
	rootSource := sourceOf(file.relative, body)
	// newMapping 会对 Key 排序，使单文件内部 Map 枚举也具有确定性。
	return newMapping(rootSource, decoded, positions, "")
}

// collectPositions 递归记录 Mapping Key、值和 Sequence 元素的来源位置。
func collectPositions(file string, node ast.Node, path string, positions map[string]sourcePos) {
	// 缺失 AST 节点没有可记录位置，直接结束当前分支。
	if node == nil {
		return
	}
	// 不同 AST 节点的子节点访问方式不同，按具体类型递归。
	switch typed := node.(type) {
	case ast.MapNode:
		// MapRange 保留解析器节点引用，用路径分别记录 Key 与 Value。
		iterator := typed.MapRange()
		for iterator.Next() {
			keyNode := iterator.Key()
			if keyNode == nil || keyNode.IsMergeKey() {
				// Anchor 合并产生的字段由解码器展开；缺少精确位置时回退到父节点。
				continue
			}
			// 配置契约只允许字符串 Key，其他 Key 留给值转换阶段报错。
			scalar, ok := keyNode.(ast.ScalarNode)
			if !ok {
				continue
			}
			key, ok := scalar.GetValue().(string)
			if !ok {
				continue
			}
			// childPath 是跨 AST、值树和合并阶段共享的内部定位键。
			childPath := joinPath(path, key)
			positions[childPath+"#key"] = sourceOf(file, keyNode)
			value := iterator.Value()
			positions[childPath] = sourceOf(file, value)
			collectPositions(file, value, childPath, positions)
		}
	case *ast.SequenceNode:
		// Sequence 用稳定索引扩展路径，并递归收集元素内部位置。
		for index, child := range typed.Values {
			childPath := sequencePath(path, index)
			positions[childPath] = sourceOf(file, child)
			collectPositions(file, child, childPath, positions)
		}
	case *ast.SequenceEntryNode:
		// Entry 只是包装节点，位置语义落在其 Value 上。
		collectPositions(file, typed.Value, path, positions)
	case *ast.AnchorNode:
		// 同文件 Anchor 的有效值仍需要沿当前逻辑路径定位。
		collectPositions(file, typed.Value, path, positions)
	case *ast.TagNode:
		// YAML Tag 不改变 Origin 内部路径，仅继续访问其包装值。
		collectPositions(file, typed.Value, path, positions)
	}
}

// sourceOf 从 AST Token 提取逻辑文件、行和列。
func sourceOf(file string, node ast.Node) sourcePos {
	// 文件级来源始终存在；缺少 Token 时保留零行列作为降级定位。
	source := sourcePos{file: file}
	if node == nil || node.GetToken() == nil || node.GetToken().Position == nil {
		return source
	}
	// Token Position 由解析器按原文件内容计算。
	source.line = node.GetToken().Position.Line
	source.column = node.GetToken().Position.Column
	return source
}

// sequencePath 为 Sequence 子元素生成内部定位路径。
func sequencePath(parent string, index int) string {
	// 即使 parent 为空也保留 [index]，便于根错误诊断。
	return parent + "[" + itoa(index) + "]"
}

// itoa 把数组索引转换为十进制路径片段。
func itoa(value int) string {
	// 配置数组索引通常很小，使用标准转换保持实现直接。
	return strconv.Itoa(value)
}

// expandEnvironment 只递归修改内部树中的字符串 Scalar。
func expandEnvironment(node *valueNode) error {
	// 节点类型决定当前值是否展开或继续递归。
	switch node.kind {
	case kindString:
		// 只把字符串交给占位符解析器，Mapping Key 和其他 Scalar 永不展开。
		value := node.scalar.(string)
		expanded, exact, err := expandString(value, os.LookupEnv)
		if err != nil {
			return invalidConfigAt(node.source, "%s", err.Error())
		}
		// 保存展开结果，并标记是否可在强类型解码时转换基础类型。
		node.scalar = expanded
		node.envDerived = exact
	case kindMapping:
		// 只递归 Mapping Value，Key 保持解析时原样。
		for index := range node.mapping {
			if err := expandEnvironment(node.mapping[index].value); err != nil {
				return err
			}
		}
	case kindSequence:
		// Sequence 保持顺序逐元素展开。
		for _, child := range node.sequence {
			if err := expandEnvironment(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// environmentLookup 抽象单次变量查询，生产使用 os.LookupEnv，测试可注入。
//
// 返回值的 bool 必须区分变量未定义与已经定义为空字符串。
type environmentLookup func(string) (string, bool)

// expandString 展开 `${NAME}` 并报告输入是否恰好只有一个占位符。
func expandString(input string, lookup environmentLookup) (string, bool, error) {
	// 没有美元符号是常见快路径，不创建 Builder。
	if !strings.Contains(input, "$") {
		return input, false, nil
	}

	// Builder 最少预留原长度；展开值较长时由标准库自动增长。
	var builder strings.Builder
	builder.Grow(len(input))
	placeholderCount := 0
	exact := false

	// 按字节扫描 ASCII 占位符语法，普通 UTF-8 字节原样复制。
	for index := 0; index < len(input); {
		if input[index] != '$' {
			builder.WriteByte(input[index])
			index++
			continue
		}

		// `$${NAME}` 是字面量转义：去掉第一个 `$`，且结果不再二次展开。
		if index+2 < len(input) && input[index+1] == '$' && input[index+2] == '{' {
			end := strings.IndexByte(input[index+3:], '}')
			if end < 0 {
				// 不完整的转义保持原文，避免把普通 `$` 文本错误判为占位符。
				builder.WriteString(input[index:])
				break
			}
			end += index + 3
			builder.WriteString(input[index+1 : end+1])
			index = end + 1
			continue
		}

		// 普通 `$` 和不支持的 `$NAME` 语法都按字面量保留。
		if index+1 >= len(input) || input[index+1] != '{' {
			builder.WriteByte(input[index])
			index++
			continue
		}
		// 真正占位符必须有右花括号，否则配置存在明确语法错误。
		end := strings.IndexByte(input[index+2:], '}')
		if end < 0 {
			return "", false, fmt.Errorf("环境变量占位符缺少右花括号")
		}
		end += index + 2
		name := input[index+2 : end]
		// 变量名限制为可移植 ASCII 标识符，拒绝默认值和命令表达式。
		if !validEnvironmentName(name) {
			return "", false, fmt.Errorf("环境变量名称无效: %s", name)
		}
		// LookupEnv 区分未定义和定义为空字符串。
		value, exists := lookup(name)
		if !exists {
			return "", false, fmt.Errorf("环境变量未定义: %s", name)
		}
		// 只写变量值，不把它带入任何错误消息。
		builder.WriteString(value)
		placeholderCount++
		exact = index == 0 && end == len(input)-1
		index = end + 1
	}
	// 只有一个且覆盖完整输入的占位符才允许后续转成非字符串类型。
	return builder.String(), placeholderCount == 1 && exact, nil
}

// validEnvironmentName 校验 `[A-Za-z_][A-Za-z0-9_]*`。
func validEnvironmentName(name string) bool {
	// 空名称和数字开头均不合法。
	if name == "" || !isEnvironmentHead(name[0]) {
		return false
	}
	// 后续字节允许首字符集合外再增加数字。
	for index := 1; index < len(name); index++ {
		character := name[index]
		if !isEnvironmentHead(character) && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// isEnvironmentHead 判断字节是否可作为环境变量名称首字符。
func isEnvironmentHead(character byte) bool {
	// 显式 ASCII 范围避免 Unicode 标识符在不同系统环境中产生差异。
	return character == '_' ||
		character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z'
}
