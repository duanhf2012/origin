package mongodbmodule

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongooptions "go.mongodb.org/mongo-driver/v2/mongo/options"
)

type moduleState uint8

const (
	stateUnconfigured moduleState = iota
	stateConfigured
	stateStarting
	stateRunning
	stateStopping
	stateStopped
)

// Module 管理一个 MongoDB Client 与默认 Database 的完整生命周期。
//
// 推荐在业务 Module 中组合 *Module，并在业务 Module.OnInit 中调用 Setup；也可以通过 New
// 构造已配置实例后交给 Service.AddModule。Module 启动成功前和停止后不暴露 Driver Handle。
type Module struct {
	service.Module

	mu            sync.RWMutex
	config        Config
	clientOptions *mongooptions.ClientOptions
	factory       runtimeFactory
	runtime       clientRuntime
	state         moduleState
}

// New 校验并冻结配置，返回可直接交给 Service.AddModule 的 MongoDB Module。
//
// New 不建立网络连接；真正的 Connect 与 Ping 在 Origin 调用 OnStart 时发生。
func New(config Config, options ...Option) (*Module, error) {
	module := &Module{}
	if err := module.configure(config, options...); err != nil {
		return nil, err
	}
	return module, nil
}

// Setup 在已绑定业务 Module 的 OnInit 中校验并冻结 MongoDB 配置。
//
// Setup 只能调用一次，且不会进行网络 I/O。若业务 Module 覆盖 OnInit，必须在其中显式调用
// Setup；通过 New 构造的独立 Module 不需要再次调用。
func (module *Module) Setup(config Config, options ...Option) error {
	if module == nil || module.Service() == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule.Setup 只能在已绑定 Module.OnInit 中调用")
	}
	return module.configure(config, options...)
}

func (module *Module) configure(config Config, options ...Option) error {
	if module == nil {
		return errs.ErrInvalidArgument
	}

	// 配置只允许从初始状态冻结一次，避免运行期替换 Client 的所有权歧义。
	module.mu.Lock()
	defer module.mu.Unlock()
	if module.state != stateUnconfigured {
		return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule 只能配置一次")
	}

	// 保存与校验使用同一份归一化配置，避免 Database 周围空格只在校验时被移除。
	config.URI = strings.TrimSpace(config.URI)
	config.Database = strings.TrimSpace(config.Database)
	config.TLSCAFile = strings.TrimSpace(config.TLSCAFile)
	clientOptions, factory, err := buildClientOptions(config, options)
	if err != nil {
		return err
	}
	module.config = config
	module.clientOptions = clientOptions
	module.factory = factory
	module.state = stateConfigured
	return nil
}

// OnInit 验证 Module 已经通过 New 或 Setup 完成配置。
func (module *Module) OnInit() error {
	if module == nil {
		return errs.ErrInvalidArgument
	}
	module.mu.RLock()
	configured := module.state == stateConfigured
	module.mu.RUnlock()
	if !configured {
		return errs.NewMessage(errs.CodeInvalidConfig, "mongodbmodule 尚未完成配置")
	}
	return nil
}

// OnStart 创建唯一 Client，并使用启动 context 对 Primary 执行 Ping。
//
// Ping 失败时会在返回前 Disconnect 已创建的 Client，避免半初始化资源泄漏。
func (module *Module) OnStart(ctx context.Context) error {
	if module == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}

	module.mu.Lock()
	if module.state != stateConfigured {
		module.mu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule 当前状态不允许启动")
	}
	module.state = stateStarting
	factory := module.factory
	clientOptions := module.clientOptions
	module.mu.Unlock()

	runtime, err := factory(clientOptions)
	if err != nil {
		module.finishFailedStart()
		return err
	}
	if runtime == nil {
		module.finishFailedStart()
		return errs.NewMessage(errs.CodeInternal, "mongodbmodule Runtime 创建结果为空")
	}
	if err = runtime.ping(ctx); err != nil {
		// 启动 context 可能已取消；仍使用同一预算尝试释放，因为官方 Disconnect 不需要网络握手。
		disconnectErr := runtime.disconnect(ctx)
		module.finishFailedStart()
		return errors.Join(err, disconnectErr)
	}

	module.mu.Lock()
	module.runtime = runtime
	module.state = stateRunning
	module.mu.Unlock()
	return nil
}

func (module *Module) finishFailedStart() {
	module.mu.Lock()
	module.runtime = nil
	module.state = stateStopped
	module.mu.Unlock()
}

// OnStop 使用停止 context 断开 Client；重复停止安全且不会再次调用 Driver。
func (module *Module) OnStop(ctx context.Context) error {
	if module == nil || ctx == nil {
		return errs.ErrInvalidArgument
	}

	module.mu.Lock()
	if module.state == stateConfigured || module.state == stateStopped || module.state == stateUnconfigured {
		module.mu.Unlock()
		return nil
	}
	if module.state != stateRunning {
		module.mu.Unlock()
		return errs.NewMessage(errs.CodeInvalidArgument, "mongodbmodule 当前状态不允许停止")
	}
	module.state = stateStopping
	runtime := module.runtime
	module.mu.Unlock()

	err := runtime.disconnect(ctx)
	module.mu.Lock()
	module.runtime = nil
	module.state = stateStopped
	module.mu.Unlock()
	return err
}

// Client 返回运行中的官方 MongoDB Client；未启动、启动失败或已停止时返回 nil。
//
// Client 适合 change stream、command 等未包装的高级能力。调用方不能 Disconnect 该 Client，
// 其所有权始终属于 Module。
func (module *Module) Client() *mongo.Client {
	runtime := module.runningRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.client()
}

// Database 返回运行中的默认官方 MongoDB Database；其他状态返回 nil。
func (module *Module) Database() *mongo.Database {
	runtime := module.runningRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.database(module.databaseName())
}

// Collection 返回默认数据库中指定名称的官方 Collection。
//
// name 为空、Module 未启动、启动失败或已经停止时返回 nil。普通 CRUD 应直接在返回的
// Collection 上调用官方 Driver 方法。
func (module *Module) Collection(name string) *mongo.Collection {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	runtime := module.runningRuntime()
	if runtime == nil {
		return nil
	}
	return runtime.collection(module.databaseName(), name)
}

// Ping 使用调用方 context 检查当前 Primary；ctx 不能为空且 Module 必须正在运行。
func (module *Module) Ping(ctx context.Context) error {
	if ctx == nil {
		return errs.ErrInvalidArgument
	}
	runtime, err := module.requireRuntime()
	if err != nil {
		return err
	}
	return runtime.ping(ctx)
}

func (module *Module) runningRuntime() clientRuntime {
	if module == nil {
		return nil
	}
	module.mu.RLock()
	defer module.mu.RUnlock()
	if module.state != stateRunning {
		return nil
	}
	return module.runtime
}

func (module *Module) requireRuntime() (clientRuntime, error) {
	runtime := module.runningRuntime()
	if runtime == nil {
		return nil, errs.NewMessage(errs.CodeServiceNotReady, "mongodbmodule 尚未运行")
	}
	return runtime, nil
}

func (module *Module) databaseName() string {
	module.mu.RLock()
	defer module.mu.RUnlock()
	return module.config.Database
}

func buildClientOptions(config Config, options []Option) (*mongooptions.ClientOptions, runtimeFactory, error) {
	config.URI = strings.TrimSpace(config.URI)
	config.Database = strings.TrimSpace(config.Database)
	config.TLSCAFile = strings.TrimSpace(config.TLSCAFile)
	if config.URI == "" || config.Database == "" {
		return nil, nil, invalidConfig("mongodbmodule URI 和 Database 均不能为空")
	}

	uri, err := inspectURI(config.URI)
	if err != nil {
		return nil, nil, err
	}
	moduleOptions := moduleOptions{factory: newDriverRuntime}
	for _, option := range options {
		if option == nil {
			return nil, nil, invalidConfig("mongodbmodule Option 不能为空")
		}
		if err := option.apply(&moduleOptions); err != nil {
			return nil, nil, err
		}
	}

	if moduleOptions.tlsConfig != nil && config.TLSCAFile != "" {
		return nil, nil, invalidConfig("mongodbmodule TLSCAFile 与 WithTLSConfig 不能同时使用")
	}
	if (moduleOptions.tlsConfig != nil || config.TLSCAFile != "") && uri.hasTLSMaterial {
		return nil, nil, invalidConfig("mongodbmodule 自定义 TLS 与 URI TLS 材料不能同时使用")
	}
	if (moduleOptions.tlsConfig != nil || config.TLSCAFile != "") && uri.explicitTLSFalse {
		return nil, nil, invalidConfig("mongodbmodule 自定义 TLS 与 URI tls=false 冲突")
	}

	// 合并顺序固定为 URI、调用顺序中的 Driver Options、最终 TLS，最后再统一校验。
	clientOptions := mongooptions.Client().ApplyURI(config.URI)
	for _, current := range moduleOptions.driverOptions {
		clientOptions = mongooptions.MergeClientOptions(clientOptions, current)
	}
	if moduleOptions.tlsConfig != nil {
		clientOptions.TLSConfig = moduleOptions.tlsConfig.Clone()
	} else if config.TLSCAFile != "" {
		tlsConfig, err := loadCA(config.TLSCAFile)
		if err != nil {
			return nil, nil, err
		}
		clientOptions.TLSConfig = tlsConfig
	}
	if clientOptions.TLSConfig != nil && clientOptions.TLSConfig.InsecureSkipVerify {
		return nil, nil, invalidConfig("mongodbmodule 禁止跳过 TLS 证书校验")
	}
	if err := clientOptions.Validate(); err != nil {
		// Driver 的原始错误可能包含 URI 片段；公共配置错误只返回脱敏阶段信息。
		return nil, nil, invalidConfig("mongodbmodule Driver 配置无效")
	}
	return clientOptions, moduleOptions.factory, nil
}

type uriInspection struct {
	hasTLSMaterial   bool
	explicitTLSFalse bool
}

func inspectURI(raw string) (uriInspection, error) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "mongodb" && parsed.Scheme != "mongodb+srv") || parsed.Host == "" {
		return uriInspection{}, invalidConfig("mongodbmodule URI 无效")
	}

	var result uriInspection
	for key, values := range parsed.Query() {
		lowerKey := strings.ToLower(key)
		switch lowerKey {
		case "tlscafile", "tlscertificatekeyfile":
			result.hasTLSMaterial = true
		case "tlsinsecure", "tlsallowinvalidcertificates", "tlsallowinvalidhostnames", "sslinvalidhostnameallowed":
			for _, value := range values {
				enabled, parseErr := strconv.ParseBool(value)
				if parseErr != nil {
					return uriInspection{}, invalidConfig("mongodbmodule URI TLS 安全选项无效")
				}
				if enabled {
					return uriInspection{}, invalidConfig("mongodbmodule URI 禁止跳过 TLS 校验")
				}
			}
		case "tls", "ssl":
			for _, value := range values {
				enabled, parseErr := strconv.ParseBool(value)
				if parseErr != nil {
					return uriInspection{}, invalidConfig("mongodbmodule URI TLS 开关无效")
				}
				if !enabled {
					result.explicitTLSFalse = true
				}
			}
		}
	}
	return result, nil
}

func loadCA(path string) (*tls.Config, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, invalidConfig("mongodbmodule 无法读取 TLS CA 文件")
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(pemData) {
		return nil, invalidConfig("mongodbmodule TLS CA 文件不包含有效证书")
	}
	return &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
}

func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}

var _ service.IModule = (*Module)(nil)
