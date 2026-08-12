package blueprintmodule

import (
	"path/filepath"
	"strings"
)

// Config 指定节点定义和蓝图文件的加载根目录。
//
// NodeDir 与 GraphDir 都是必填字段；相对路径在 Setup 时基于进程当前工作目录转换为绝对路径。目录读取、
// JSON 解析和蓝图编译统一在 OnStart 执行，Setup 本身不进行文件 I/O。
type Config struct {
	// NodeDir 是节点定义 JSON 根目录；引擎按自身规则递归加载。
	NodeDir string
	// GraphDir 是 .vgf、.obp 和 .obpf 蓝图根目录；引擎按自身规则递归加载。
	GraphDir string
}

func normalizeConfig(input Config) (Config, error) {
	// 配置字段只接受非空路径；先清理空白，避免保存与实际验证不同的字符串。
	input.NodeDir = strings.TrimSpace(input.NodeDir)
	input.GraphDir = strings.TrimSpace(input.GraphDir)
	if input.NodeDir == "" || input.GraphDir == "" {
		return Config{}, invalidConfig("blueprintmodule node_dir 和 graph_dir 都不能为空")
	}

	// 冻结绝对路径，使运行期 Reload 不受进程工作目录后续变化影响。
	nodeDir, err := filepath.Abs(filepath.Clean(input.NodeDir))
	if err != nil {
		return Config{}, invalidConfig("blueprintmodule node_dir 无法转换为绝对路径")
	}
	graphDir, err := filepath.Abs(filepath.Clean(input.GraphDir))
	if err != nil {
		return Config{}, invalidConfig("blueprintmodule graph_dir 无法转换为绝对路径")
	}
	input.NodeDir = nodeDir
	input.GraphDir = graphDir
	return input, nil
}
