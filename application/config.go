package application

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	originconfig "github.com/duanhf2012/origin/v3/config"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	internaldiscovery "github.com/duanhf2012/origin/v3/internal/discovery"
	etcddiscovery "github.com/duanhf2012/origin/v3/internal/discovery/etcd"
	origindiscovery "github.com/duanhf2012/origin/v3/internal/discovery/origin"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// loadedConfig 是 Application 启动过程消费的框架配置快照。
type loadedConfig struct {
	root            *originconfig.Snapshot
	log             originlog.Config
	trackBufferPool bool
	nodes           []node.Config
	discovery       *discoverySelection
}

// discoverySelection 保存顶层严格联合已经选中的唯一 Provider 配置块。
type discoverySelection struct {
	kind       string
	config     publicprovider.Config
	configRoot string
}

// bufferPoolConfig 只包含当前对外提供的内存池开关。
type bufferPoolConfig struct {
	TrackUsage bool `json:"track_usage"`
}

// nodeConfig 与公开 node.Config 分离，使配置 Tag 不污染运行时对象。
type nodeConfig struct {
	ID             string                 `json:"id"`
	Private        bool                   `json:"private"`
	Labels         map[string]string      `json:"labels"`
	AllowDiscovery json.RawMessage        `json:"allow_discovery"`
	Scheduler      *schedulerConfigMirror `json:"scheduler"`
	RPC            *nodeRPCConfigMirror   `json:"rpc"`
	Services       []string               `json:"services"`
}

// discoveryRuleMirror 保留关注规则两个可选维度的“省略”和“显式空值”区别。
type discoveryRuleMirror struct {
	Services   *[]string                        `json:"services"`
	NodeLabels *map[string]discoveryLabelValues `json:"node_labels"`
}

// discoveryLabelValues 把配置中的单个标签值和标签值列表统一转换为 Slice。
type discoveryLabelValues []string

// UnmarshalJSON 只接受一个字符串或字符串数组，不把数字、布尔值等隐式转成文本。
func (values *discoveryLabelValues) UnmarshalJSON(data []byte) error {
	// 优先解析单值外观，使常见的 region: cn-east 保持简洁。
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*values = discoveryLabelValues{single}
		return nil
	}

	// 单值不成立时严格解析字符串数组；元素类型错误由标准库返回明确路径。
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*values = discoveryLabelValues(multiple)
	return nil
}

// rpcConfigMirror 保存 Application 级共享 RPC 配置。连接参数只在这里声明一次。
type rpcConfigMirror struct {
	Transport        string                 `json:"transport"`
	MaxPayloadSize   *originconfig.ByteSize `json:"max_payload_size"`
	MaxBroadcastSize *originconfig.ByteSize `json:"max_broadcast_size"`
	TCP              *rpcTCPConfigMirror    `json:"tcp"`
	NATS             *rpcNATSConfigMirror   `json:"nats"`
}

// rpcTCPConfigMirror 使用指针保留“省略字段沿用默认值”的明确语义。
type rpcTCPConfigMirror struct {
	SendQueueMessages *int                   `json:"send_queue_messages"`
	ReadIdleTimeout   *originconfig.Duration `json:"read_idle_timeout"`
	WriteTimeout      *originconfig.Duration `json:"write_timeout"`
}

// nodeRPCConfigMirror 只允许 TCP Node 声明自身的监听和对外地址。
//
// NATS 使用共享 Application 连接配置；因此 nodes[].rpc 在 NATS 模式下必须省略。
type nodeRPCConfigMirror struct {
	TCP *nodeRPCTCPConfigMirror `json:"tcp"`
}

type nodeRPCTCPConfigMirror struct {
	Listen    string `json:"listen"`
	Advertise string `json:"advertise"`
}

// rpcNATSConfigMirror 只暴露项目确实需要选择的 Namespace、Server、接收队列、认证和 TLS。
type rpcNATSConfigMirror struct {
	Namespace            string                  `json:"namespace"`
	URLs                 []string                `json:"urls"`
	ReceiveQueueMessages *int                    `json:"receive_queue_messages"`
	Auth                 rpcNATSAuthConfigMirror `json:"auth"`
	TLS                  rpcNATSTLSConfigMirror  `json:"tls"`
}

// rpcNATSAuthConfigMirror 与 NATS 官方四种互斥认证方式一一对应。
type rpcNATSAuthConfigMirror struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	Token           string `json:"token"`
	CredentialsFile string `json:"credentials_file"`
	NKeySeedFile    string `json:"nkey_seed_file"`
}

// rpcNATSTLSConfigMirror 使用与其他 TLS 配置一致的稳定字段名。
type rpcNATSTLSConfigMirror struct {
	Enabled            bool   `json:"enabled"`
	CAFile             string `json:"ca_file"`
	CertFile           string `json:"cert_file"`
	KeyFile            string `json:"key_file"`
	ServerName         string `json:"server_name"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify"`
}

// schedulerConfigMirror 使用指针区分“字段省略”和用户显式写入的零值。
//
// 这种表示只存在于配置冷路径；运行时统一转换为不含指针的 service.SchedulerConfig。
type schedulerConfigMirror struct {
	MaxTasks            *int                   `json:"max_tasks"`
	MaxAwaitTasks       *int                   `json:"max_await_tasks"`
	DefaultAwaitTimeout *originconfig.Duration `json:"default_await_timeout"`
}

// logConfigMirror 使用字符串和公开 config 值类型承接用户配置。
type logConfigMirror struct {
	Mode    string              `json:"mode"`
	Console consoleConfigMirror `json:"console"`
	File    fileConfigMirror    `json:"file"`
}

type consoleConfigMirror struct {
	Enabled       bool                      `json:"enabled"`
	Level         string                    `json:"level"`
	Format        string                    `json:"format"`
	ContextFields contextFieldsConfigMirror `json:"context_fields"`
}

// contextFieldsConfigMirror 使用预填充 bool 保存“省略为 true、显式 false 覆盖”的语义。
type contextFieldsConfigMirror struct {
	NodeID      bool `json:"node_id"`
	ServiceName bool `json:"service_name"`
}

type fileConfigMirror struct {
	Enabled       bool                      `json:"enabled"`
	Level         string                    `json:"level"`
	Format        string                    `json:"format"`
	Path          string                    `json:"path"`
	ContextFields contextFieldsConfigMirror `json:"context_fields"`
	Rotation      rotationConfigMirror      `json:"rotation"`
	Retention     retentionConfigMirror     `json:"retention"`
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

// loadConfig 读取整个配置目录，再只解析当前具有运行语义的框架字段。
func loadConfig(directory string) (loadedConfig, error) {
	snapshot, err := originconfig.LoadSnapshot(directory)
	if err != nil {
		return loadedConfig{}, err
	}
	var root map[string]any
	if err := snapshot.Decode(&root); err != nil {
		return loadedConfig{}, err
	}

	// 容易被误认为框架配置、但当前不受支持的顶层字段不能被静默忽略。
	for _, name := range []string{"timer"} {
		if _, exists := root[name]; exists {
			return loadedConfig{}, invalidConfigf(
				"顶层框架配置字段 %q 不受支持",
				name,
			)
		}
	}

	result := loadedConfig{
		root: snapshot,
		log:  originlog.DefaultConfig(),
	}
	var applicationRPC *rpc.Config
	if raw, exists := root["rpc"]; exists {
		configured, err := decodeApplicationRPCConfig(raw)
		if err != nil {
			return loadedConfig{}, err
		}
		applicationRPC = &configured
	}
	if raw, exists := root["discovery"]; exists {
		selection, err := decodeDiscoverySelection(raw)
		if err != nil {
			return loadedConfig{}, err
		}
		selection.configRoot, err = filepath.Abs(directory)
		if err != nil {
			return loadedConfig{}, errs.Wrap(errs.CodeInvalidConfig, err)
		}
		if selection.kind == "etcd" {
			if _, err := etcddiscovery.DecodeConfig(
				selection.config,
				selection.configRoot,
			); err != nil {
				return loadedConfig{}, err
			}
		}
		result.discovery = selection
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
		if !validProviderName(configured.ID) {
			return loadedConfig{}, invalidConfigf(
				"nodes[%d].id 必须是 63 字节以内的小写 kebab-case",
				index,
			)
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
		if err := internaldiscovery.ValidateNodeLabels(configured.Labels); err != nil {
			return loadedConfig{}, invalidConfigf(
				"Node %q 的 labels 无效: %v",
				configured.ID,
				err,
			)
		}
		seen[configured.ID] = struct{}{}
		schedulerConfig := service.DefaultSchedulerConfig()
		if configured.Scheduler != nil {
			// 从稳定默认值开始逐项覆盖，允许项目只调整真正关心的容量或超时。
			if configured.Scheduler.MaxTasks != nil {
				schedulerConfig.MaxTasks = *configured.Scheduler.MaxTasks
			}
			if configured.Scheduler.MaxAwaitTasks != nil {
				schedulerConfig.MaxAwaitTasks = *configured.Scheduler.MaxAwaitTasks
			}
			if configured.Scheduler.DefaultAwaitTimeout != nil {
				schedulerConfig.DefaultAwaitTimeout =
					configured.Scheduler.DefaultAwaitTimeout.Duration()
			}
		}
		// 配置错误应在 Application 构造阶段暴露，而不是等到某个 Service 已经 OnStart 后
		// 才由 Scheduler 装配失败触发整组回滚。
		if err := schedulerConfig.Validate(); err != nil {
			return loadedConfig{}, invalidConfigf(
				"Node %q 的 scheduler 配置无效: %v",
				configured.ID,
				err,
			)
		}
		rpcConfig, err := resolveNodeRPCConfig(
			configured.ID,
			configured.RPC,
			applicationRPC,
		)
		if err != nil {
			return loadedConfig{}, err
		}
		discoveryFilter, err := decodeDiscoveryFilter(
			configured.ID,
			configured.AllowDiscovery,
		)
		if err != nil {
			return loadedConfig{}, err
		}
		result.nodes[index] = node.Config{
			ID:              configured.ID,
			Private:         configured.Private,
			Labels:          cloneStringMap(configured.Labels),
			DiscoveryFilter: discoveryFilter,
			Scheduler:       schedulerConfig,
			RPC:             rpcConfig,
			Services:        append([]string(nil), configured.Services...),
		}
	}
	if err := validateOriginDiscovery(result.discovery, nodes, result.nodes); err != nil {
		return loadedConfig{}, err
	}
	return result, nil
}

// decodeDiscoverySelection 严格解析 type 与唯一同名配置块。
func decodeDiscoverySelection(raw any) (*discoverySelection, error) {
	mapping, ok := raw.(map[string]any)
	if !ok {
		return nil, invalidConfigf("顶层 discovery 必须是 Mapping")
	}
	rawKind, exists := mapping["type"]
	kind, ok := rawKind.(string)
	kind = strings.TrimSpace(kind)
	if !exists || !ok || !validProviderName(kind) {
		return nil, invalidConfigf(
			"discovery.type 必须是 63 字节以内的小写 kebab-case",
		)
	}
	block, exists := mapping[kind]
	if !exists {
		return nil, invalidConfigf("discovery 缺少选中的 %q 配置块", kind)
	}
	for key := range mapping {
		if key != "type" && key != kind {
			return nil, invalidConfigf(
				"discovery 不能同时配置未选中的 Provider 块 %q",
				key,
			)
		}
	}
	config, err := publicprovider.NewConfig(block)
	if err != nil {
		return nil, err
	}
	return &discoverySelection{kind: kind, config: config}, nil
}

// validateOriginDiscovery 在创建任何 Node 前校验唯一保留 Service 和 server.node。
func validateOriginDiscovery(
	selection *discoverySelection,
	nodes []nodeConfig,
	decoded []node.Config,
) error {
	var originConfig origindiscovery.Config
	if selection != nil && selection.kind == "origin" {
		var err error
		originConfig, err = origindiscovery.DecodeConfig(selection.config)
		if err != nil {
			return err
		}
	}
	foundServer := false
	foundService := false
	for index, configured := range nodes {
		if selection != nil && selection.kind == "origin" &&
			configured.ID == originConfig.Server.Node {
			foundServer = true
		}
		if selection != nil && selection.kind == "origin" && decoded[index].RPC == nil {
			return invalidConfigf(
				"使用 discovery.origin 时必须配置顶层 rpc",
			)
		}
		for _, declaration := range configured.Services {
			name, template, private, err := parseServiceDeclaration(declaration)
			if err != nil {
				return err
			}
			if template != "DiscoveryService" {
				continue
			}
			if foundService || selection == nil || selection.kind != "origin" ||
				configured.ID != originConfig.Server.Node ||
				name != "DiscoveryService" || private {
				return invalidConfigf(
					"DiscoveryService 必须唯一且以公开原名配置在 discovery.origin.server.node",
				)
			}
			foundService = true
		}
	}
	if selection != nil && selection.kind == "origin" &&
		(!foundServer || !foundService) {
		return invalidConfigf(
			"discovery.origin.server.node 必须存在并包含唯一 DiscoveryService",
		)
	}
	return nil
}

func validProviderName(value string) bool {
	if len(value) == 0 || len(value) > 63 ||
		value[0] < 'a' || value[0] > 'z' ||
		value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z':
			previousDash = false
		case character >= '0' && character <= '9':
			previousDash = false
		case character == '-' && !previousDash:
			previousDash = true
		default:
			return false
		}
	}
	return true
}

// decodeDiscoveryFilter 区分字段省略、显式 null、空列表和非空规则，并完成冷路径预编译。
func decodeDiscoveryFilter(
	nodeID string,
	raw json.RawMessage,
) (internaldiscovery.Filter, error) {
	// RawMessage 为 nil 只表示字段完全省略，此时采用全量公开发现的当前默认值。
	if len(raw) == 0 {
		return internaldiscovery.CompileFilter(false, nil)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return internaldiscovery.Filter{}, invalidConfigf(
			"Node %q 的 allow_discovery 不能为 null",
			nodeID,
		)
	}

	// 对规则数组启用未知字段拒绝，避免拼写错误静默退化为另一种匹配范围。
	var mirrors []discoveryRuleMirror
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&mirrors); err != nil {
		return internaldiscovery.Filter{}, invalidConfigf(
			"解析 Node %q 的 allow_discovery: %v",
			nodeID,
			err,
		)
	}
	rules := make([]internaldiscovery.Rule, len(mirrors))
	for index, mirror := range mirrors {
		rules[index].Services = mirror.Services
		if mirror.NodeLabels == nil {
			continue
		}

		// 转换后每条规则独占自己的 Map 和 Slice，配置镜像不会泄漏到运行时。
		labels := make(map[string][]string, len(*mirror.NodeLabels))
		for key, values := range *mirror.NodeLabels {
			labels[key] = append([]string(nil), values...)
		}
		rules[index].NodeLabels = &labels
	}
	filter, err := internaldiscovery.CompileFilter(true, rules)
	if err != nil {
		return internaldiscovery.Filter{}, err
	}
	return filter, nil
}

// cloneStringMap 冻结 Node 标签，避免配置根 Map 被项目代码修改后污染运行时。
func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// decodeApplicationRPCConfig 冻结一次 Application 级传输参数。TCP 的地址属于具体 Node，
// 在 resolveNodeRPCConfig 中再合成完整运行时配置。
func decodeApplicationRPCConfig(raw any) (rpc.Config, error) {
	var mirror rpcConfigMirror
	if err := decodeSection("rpc", raw, &mirror); err != nil {
		return rpc.Config{}, err
	}
	transport := strings.ToLower(strings.TrimSpace(mirror.Transport))
	if transport == "" {
		transport = rpc.TransportTCP
	}
	maxPayloadSize, err := decodeRPCPayloadSize(mirror.MaxPayloadSize)
	if err != nil {
		return rpc.Config{}, err
	}
	maxBroadcastSize, err := decodeRPCBroadcastSize(mirror.MaxBroadcastSize)
	if err != nil {
		return rpc.Config{}, err
	}
	result := rpc.Config{
		Transport:        transport,
		MaxPayloadSize:   maxPayloadSize,
		MaxBroadcastSize: maxBroadcastSize,
	}
	switch transport {
	case rpc.TransportTCP:
		if mirror.TCP == nil {
			return rpc.Config{}, invalidConfigf("rpc.transport 为 tcp 时 rpc.tcp 不能为空")
		}
		if mirror.NATS != nil {
			return rpc.Config{}, invalidConfigf("rpc.transport 为 tcp 时不能配置 rpc.nats")
		}
		result.TCP = rpc.DefaultTCPConfig()
		if mirror.TCP.SendQueueMessages != nil {
			result.TCP.SendQueueMessages = *mirror.TCP.SendQueueMessages
		}
		if mirror.TCP.ReadIdleTimeout != nil {
			result.TCP.ReadIdleTimeout = mirror.TCP.ReadIdleTimeout.Duration()
		}
		if mirror.TCP.WriteTimeout != nil {
			result.TCP.WriteTimeout = mirror.TCP.WriteTimeout.Duration()
		}
		// Validate 共享 TCP 参数时提供临时合法地址；每个 Node 的实际地址会在随后验证。
		result.TCP.Listen = "127.0.0.1:1"
		result.TCP.Advertise = "127.0.0.1:1"
	case rpc.TransportNATS:
		if mirror.NATS == nil {
			return rpc.Config{}, invalidConfigf("rpc.transport 为 nats 时 rpc.nats 不能为空")
		}
		if mirror.TCP != nil {
			return rpc.Config{}, invalidConfigf("rpc.transport 为 nats 时不能配置 rpc.tcp")
		}
		result.NATS = decodeRPCNATSConfig(*mirror.NATS)
	default:
		return rpc.Config{}, invalidConfigf("rpc.transport 必须是 tcp 或 nats")
	}
	if err := result.Validate(); err != nil {
		return rpc.Config{}, invalidConfigf("rpc 配置无效: %v", err)
	}
	return result, nil
}

func decodeRPCPayloadSize(value *originconfig.ByteSize) (int, error) {
	if value == nil {
		return rpc.DefaultMaxPayloadSize, nil
	}
	size := value.Bytes()
	if size <= 0 || uint64(size) > uint64(^uint(0)>>1) {
		return 0, invalidConfigf("rpc.max_payload_size 无法由当前平台 int 表达")
	}
	return int(size), nil
}

func decodeRPCBroadcastSize(value *originconfig.ByteSize) (int, error) {
	if value == nil {
		return rpc.DefaultMaxBroadcastSize, nil
	}
	size := value.Bytes()
	if size <= 0 || size > int64(rpc.MaxBroadcastSize) ||
		uint64(size) > uint64(^uint(0)>>1) {
		return 0, invalidConfigf("rpc.max_broadcast_size 必须位于 1B～1G 且能由当前平台 int 表达")
	}
	return int(size), nil
}

func decodeRPCNATSConfig(mirror rpcNATSConfigMirror) *rpc.NATSConfig {
	result := rpc.DefaultNATSConfig()
	result.Namespace = strings.TrimSpace(mirror.Namespace)
	result.URLs = append([]string(nil), mirror.URLs...)
	if mirror.ReceiveQueueMessages != nil {
		result.ReceiveQueueMessages = *mirror.ReceiveQueueMessages
	}
	result.Auth = rpc.NATSAuthConfig{
		Username:        mirror.Auth.Username,
		Password:        mirror.Auth.Password,
		Token:           mirror.Auth.Token,
		CredentialsFile: mirror.Auth.CredentialsFile,
		NKeySeedFile:    mirror.Auth.NKeySeedFile,
	}
	result.TLS = rpc.NATSTLSConfig{
		Enabled:            mirror.TLS.Enabled,
		CAFile:             mirror.TLS.CAFile,
		CertFile:           mirror.TLS.CertFile,
		KeyFile:            mirror.TLS.KeyFile,
		ServerName:         mirror.TLS.ServerName,
		InsecureSkipVerify: mirror.TLS.InsecureSkipVerify,
	}
	return result
}

// resolveNodeRPCConfig 合成 Node 的独立完整 RPC 快照，避免各 Node 共享可变指针。
func resolveNodeRPCConfig(
	nodeID string,
	mirror *nodeRPCConfigMirror,
	applicationRPC *rpc.Config,
) (*rpc.Config, error) {
	if applicationRPC == nil {
		if mirror != nil {
			return nil, invalidConfigf("Node %q 配置 rpc 前必须先配置顶层 rpc", nodeID)
		}
		return nil, nil
	}
	result := cloneRPCConfig(*applicationRPC)
	switch result.Transport {
	case rpc.TransportTCP:
		if mirror == nil || mirror.TCP == nil {
			return nil, invalidConfigf("Node %q 在 TCP 模式下必须配置 rpc.tcp.listen 和 rpc.tcp.advertise", nodeID)
		}
		result.TCP.Listen = strings.TrimSpace(mirror.TCP.Listen)
		result.TCP.Advertise = strings.TrimSpace(mirror.TCP.Advertise)
	case rpc.TransportNATS:
		if mirror != nil {
			return nil, invalidConfigf("Node %q 在 NATS 模式下不能配置 rpc；请使用顶层 rpc.nats", nodeID)
		}
	default:
		return nil, invalidConfigf("rpc.transport 必须是 tcp 或 nats")
	}
	if err := result.Validate(); err != nil {
		return nil, invalidConfigf("Node %q 的 rpc 配置无效: %v", nodeID, err)
	}
	return &result, nil
}

func cloneRPCConfig(source rpc.Config) rpc.Config {
	result := source
	if source.TCP != nil {
		copied := *source.TCP
		result.TCP = &copied
	}
	if source.NATS != nil {
		copied := *source.NATS
		copied.URLs = append([]string(nil), source.NATS.URLs...)
		result.NATS = &copied
	}
	return result
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
			ContextFields: contextFieldsConfigMirror{
				NodeID:      defaults.Console.ContextFields.NodeID,
				ServiceName: defaults.Console.ContextFields.ServiceName,
			},
		},
		File: fileConfigMirror{
			Enabled: defaults.File.Enabled,
			Level:   defaults.File.Level.String(),
			Format:  string(defaults.File.Format),
			Path:    defaults.File.Path,
			ContextFields: contextFieldsConfigMirror{
				NodeID:      defaults.File.ContextFields.NodeID,
				ServiceName: defaults.File.ContextFields.ServiceName,
			},
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
	result.Console.ContextFields = originlog.ContextFieldsConfig{
		NodeID:      mirror.Console.ContextFields.NodeID,
		ServiceName: mirror.Console.ContextFields.ServiceName,
	}
	result.File.ContextFields = originlog.ContextFieldsConfig{
		NodeID:      mirror.File.ContextFields.NodeID,
		ServiceName: mirror.File.ContextFields.ServiceName,
	}

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

// parseLogFormat 只接受当前内置的两种稳定格式名。
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
