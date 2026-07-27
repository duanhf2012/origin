package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
)

// loadedConfig 是 M7 真正消费的框架配置快照。
type loadedConfig struct {
	root            map[string]any
	log             originlog.Config
	trackBufferPool bool
	nodes           []node.Config
}

// bufferPoolConfig 只包含 M7 已实现的内存池开关。
type bufferPoolConfig struct {
	TrackUsage bool `json:"track_usage"`
}

// nodeConfig 与公开 node.Config 分离，使配置 Tag 不污染运行时对象。
type nodeConfig struct {
	ID       string   `json:"id"`
	Private  bool     `json:"private"`
	Services []string `json:"services"`
}

// logConfigMirror 使用字符串和公开 config 值类型承接用户配置。
type logConfigMirror struct {
	Mode    string              `json:"mode"`
	Console consoleConfigMirror `json:"console"`
	File    fileConfigMirror    `json:"file"`
}

type consoleConfigMirror struct {
	Enabled bool   `json:"enabled"`
	Level   string `json:"level"`
	Format  string `json:"format"`
}

type fileConfigMirror struct {
	Enabled   bool                  `json:"enabled"`
	Level     string                `json:"level"`
	Format    string                `json:"format"`
	Path      string                `json:"path"`
	Rotation  rotationConfigMirror  `json:"rotation"`
	Retention retentionConfigMirror `json:"retention"`
}

type rotationConfigMirror struct {
	MaxSize  originconfig.ByteSize `json:"max_size"`
	ByDate   bool                  `json:"by_date"`
	Timezone string                `json:"timezone"`
}

type retentionConfigMirror struct {
	MaxAge   originconfig.Duration `json:"max_age"`
	MaxFiles int                   `json:"max_files"`
	Compress bool                  `json:"compress"`
}

// loadConfig 读取整个配置目录，再只解析 M7 已经拥有运行语义的框架字段。
func loadConfig(directory string) (loadedConfig, error) {
	var root map[string]any
	if err := originconfig.LoadDir(directory, &root); err != nil {
		return loadedConfig{}, err
	}

	// 已经保留给后续里程碑的框架字段不能在 M7 被静默忽略。
	for _, name := range []string{"rpc", "discovery", "timer"} {
		if _, exists := root[name]; exists {
			return loadedConfig{}, invalidConfigf(
				"配置字段 %q 尚未在 M7 实现",
				name,
			)
		}
	}

	result := loadedConfig{
		root: root,
		log:  originlog.DefaultConfig(),
	}
	if raw, exists := root["log"]; exists {
		logConfig, err := decodeLogConfig(raw)
		if err != nil {
			return loadedConfig{}, err
		}
		result.log = logConfig
	}
	if raw, exists := root["buffer_pool"]; exists {
		var pool bufferPoolConfig
		if err := decodeSection("buffer_pool", raw, &pool); err != nil {
			return loadedConfig{}, err
		}
		result.trackBufferPool = pool.TrackUsage
	}

	rawNodes, exists := root["nodes"]
	if !exists {
		return loadedConfig{}, invalidConfigf("配置缺少顶层 nodes")
	}
	var nodes []nodeConfig
	if err := decodeSection("nodes", rawNodes, &nodes); err != nil {
		return loadedConfig{}, err
	}
	if len(nodes) == 0 {
		return loadedConfig{}, invalidConfigf("nodes 不能为空")
	}
	result.nodes = make([]node.Config, len(nodes))
	seen := make(map[string]struct{}, len(nodes))
	for index, configured := range nodes {
		configured.ID = strings.TrimSpace(configured.ID)
		if configured.ID == "" {
			return loadedConfig{}, invalidConfigf("nodes[%d].id 不能为空", index)
		}
		if _, duplicate := seen[configured.ID]; duplicate {
			return loadedConfig{}, invalidConfigf("NodeID %q 重复", configured.ID)
		}
		if len(configured.Services) == 0 {
			return loadedConfig{}, invalidConfigf(
				"Node %q 的 services 不能为空",
				configured.ID,
			)
		}
		seen[configured.ID] = struct{}{}
		result.nodes[index] = node.Config{
			ID:       configured.ID,
			Private:  configured.Private,
			Services: append([]string(nil), configured.Services...),
		}
	}
	return result, nil
}

// decodeLogConfig 从公开默认值开始覆盖字段，未声明项自然沿用稳定默认。
func decodeLogConfig(raw any) (originlog.Config, error) {
	defaults := originlog.DefaultConfig()
	mirror := logConfigMirror{
		Mode: logModeName(defaults.Mode),
		Console: consoleConfigMirror{
			Enabled: defaults.Console.Enabled,
			Level:   defaults.Console.Level.String(),
			Format:  string(defaults.Console.Format),
		},
		File: fileConfigMirror{
			Enabled: defaults.File.Enabled,
			Level:   defaults.File.Level.String(),
			Format:  string(defaults.File.Format),
			Path:    defaults.File.Path,
			Rotation: rotationConfigMirror{
				MaxSize:  originconfig.ByteSize(defaults.File.Rotation.MaxSizeMB * 1024 * 1024),
				ByDate:   defaults.File.Rotation.ByDate,
				Timezone: string(defaults.File.Rotation.Timezone),
			},
			Retention: retentionConfigMirror{
				MaxAge:   originconfig.Duration(time.Duration(defaults.File.Retention.MaxAgeDays) * 24 * time.Hour),
				MaxFiles: defaults.File.Retention.MaxFiles,
				Compress: defaults.File.Retention.Compress,
			},
		},
	}
	if err := decodeSection("log", raw, &mirror); err != nil {
		return originlog.Config{}, err
	}

	result := defaults
	switch strings.ToLower(mirror.Mode) {
	case "async":
		result.Mode = originlog.AsyncMode
	case "sync":
		result.Mode = originlog.SyncMode
	default:
		return originlog.Config{}, invalidConfigf("log.mode 必须是 async 或 sync")
	}
	result.Console.Enabled = mirror.Console.Enabled
	result.File.Enabled = mirror.File.Enabled

	// 控制台和文件即使关闭也解析其显式字段，避免打开后才暴露拼写错误。
	consoleLevel, ok := originlog.ParseLevel(mirror.Console.Level)
	if !ok {
		return originlog.Config{}, invalidConfigf("log.console.level 无效")
	}
	fileLevel, ok := originlog.ParseLevel(mirror.File.Level)
	if !ok {
		return originlog.Config{}, invalidConfigf("log.file.level 无效")
	}
	consoleFormat, err := parseLogFormat("log.console.format", mirror.Console.Format)
	if err != nil {
		return originlog.Config{}, err
	}
	fileFormat, err := parseLogFormat("log.file.format", mirror.File.Format)
	if err != nil {
		return originlog.Config{}, err
	}
	result.Console.Level = consoleLevel
	result.Console.Format = consoleFormat
	result.File.Level = fileLevel
	result.File.Format = fileFormat
	result.File.Path = mirror.File.Path

	const bytesPerMiB = int64(1024 * 1024)
	maxSizeBytes := mirror.File.Rotation.MaxSize.Bytes()
	if maxSizeBytes < 0 || maxSizeBytes%bytesPerMiB != 0 {
		return originlog.Config{}, invalidConfigf(
			"log.file.rotation.max_size 必须是非负且能整除 1M 的字节大小",
		)
	}
	result.File.Rotation.MaxSizeMB = maxSizeBytes / bytesPerMiB
	result.File.Rotation.ByDate = mirror.File.Rotation.ByDate
	switch strings.ToLower(mirror.File.Rotation.Timezone) {
	case "local":
		result.File.Rotation.Timezone = originlog.LocalTime
	case "utc":
		result.File.Rotation.Timezone = originlog.UTCTime
	default:
		return originlog.Config{}, invalidConfigf(
			"log.file.rotation.timezone 必须是 Local 或 UTC",
		)
	}

	maxAge := mirror.File.Retention.MaxAge.Duration()
	if maxAge < 0 || maxAge%(24*time.Hour) != 0 {
		return originlog.Config{}, invalidConfigf(
			"log.file.retention.max_age 必须是非负整天时长",
		)
	}
	maxAgeDays := int64(maxAge / (24 * time.Hour))
	if maxAgeDays > int64(^uint(0)>>1) {
		return originlog.Config{}, invalidConfigf("log.file.retention.max_age 过大")
	}
	result.File.Retention.MaxAgeDays = int(maxAgeDays)
	result.File.Retention.MaxFiles = mirror.File.Retention.MaxFiles
	result.File.Retention.Compress = mirror.File.Retention.Compress
	if err := result.Validate(); err != nil {
		return originlog.Config{}, err
	}
	return result, nil
}

// logModeName 把公开枚举转换为配置中的稳定小写名称。
func logModeName(mode originlog.Mode) string {
	if mode == originlog.SyncMode {
		return "sync"
	}
	return "async"
}

// decodeSection 通过标准 JSON 严格解码一个已经合并的配置节点。
func decodeSection(name string, raw any, target any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return invalidConfigf("编码配置字段 %q: %v", name, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return invalidConfigf("解析配置字段 %q: %v", name, err)
	}
	return nil
}

// parseLogFormat 只接受 M1 内置的两种稳定格式名。
func parseLogFormat(path, value string) (originlog.Format, error) {
	switch strings.ToLower(value) {
	case string(originlog.TextFormat):
		return originlog.TextFormat, nil
	case string(originlog.JSONFormat):
		return originlog.JSONFormat, nil
	default:
		return "", invalidConfigf("%s 必须是 text 或 json", path)
	}
}

// invalidConfigf 创建带配置错误码的动态诊断。
func invalidConfigf(format string, arguments ...any) error {
	return errs.NewMessage(errs.CodeInvalidConfig, fmt.Sprintf(format, arguments...))
}
