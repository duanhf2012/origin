// Package contracts_test 固定教程直接使用的 Origin 外观。
//
// 本文件只做编译期契约检查，不复制行为测试。它有意不锁定 node.New、rpc.Runtime、
// rpc.Reader/Writer/Sizer 和 service 包的框架装配函数；这些导出项属于包间集成层，
// 不是普通项目在教程中直接使用的外观。
package contracts_test

import (
	"context"
	"net/http"
	"time"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/command"
	"github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/diagnostics"
	"github.com/duanhf2012/origin/v3/discovery"
	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	tutorialrpc "github.com/duanhf2012/origin/v3/examples/_support/tutorialrpc"
	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/node"
	"github.com/duanhf2012/origin/v3/rpc"
	"github.com/duanhf2012/origin/v3/service"
)

// applicationFacade 固定教程从 Application 完成装配、运行、管理和诊断的入口。
type applicationFacade interface {
	Setup(...service.IService)
	RegisterCommand(command.Command) error
	RegisterDiscoveryProvider(string, publicprovider.Factory) error
	RegisterAdminEndpoint(admin.Endpoint) error
	SetAdminGuard(admin.Guard) error
	Start()
	Stop(context.Context) error
	State() application.State
	Node(string) (*node.Node, bool)
	Nodes() []*node.Node
	Logger() originlog.Logger
	Diagnostics() diagnostics.Snapshot
	DiagnosticsSummary() diagnostics.Summary
	Retire(context.Context) error
	Resume(context.Context) error
	StartAdminServer(string) error
	StopAdminServer(context.Context) error
	AdminAddress() (string, bool)
	StartPprof(string) error
	StopPprof(context.Context) error
	PprofAddress() (string, bool)
}

// serviceFacade 固定业务 Service 在生命周期、调度、配置、事件、Timer 和发现上的主外观。
type serviceFacade interface {
	service.IService
	NodeID() string
	State() service.State
	Logger() originlog.Logger
	Failure() error
	LookupLocalService(string) (service.IService, bool)
	Application() service.ApplicationRuntime
	GetConfig(string, any) error
	GetServiceConfig(string, any) error
	ParseServiceConfig(any) error
	AddDiscoveryListener(discovery.IListener) (discovery.ListenerID, error)
	RemoveDiscoveryListener(*discovery.ListenerID) bool
	FindDiscoveredService(string, string) (discovery.Instance, bool)
	ListDiscoveredServices(string) []discovery.Instance
	AwaitService(context.Context, string) error
	AwaitNodeService(context.Context, string, string) error
}

// moduleFacade 固定 Module 对所属 Service 能力的委托；Module 不形成第二套运行模型。
type moduleFacade interface {
	service.IModule
	Service() service.IService
	GetNode() service.NodeRuntime
	Logger() originlog.Logger
	GetConfig(string, any) error
	GetServiceConfig(string, any) error
	ParseServiceConfig(any) error
	DispatchAsync(func(context.Context)) error
	Await(context.Context, func(context.Context) error) error
	SetDefaultAwaitTimeout(time.Duration) error
	GoSafe(func()) error
	RunSafe(func()) error
	AddModule(service.IModule) error
	SubscribeEvent(service.EventID, service.EventHandler) error
	NotifyEventAsync(service.Event) error
	NotifyEventSync(context.Context, service.Event) error
	Retire(context.Context) error
	Resume(context.Context) error
	service.ITimer
}

// commandRunnerFacade 固定可嵌入进程入口的命令解析和自定义命令能力。
type commandRunnerFacade interface {
	Register(command.Command) error
	Run(context.Context, []string) (command.ExitCode, error)
}

// generatedPlayerClientFacade 使用教程真实生成物固定 RPC 客户端的调用和派生外观。
type generatedPlayerClientFacade interface {
	OnNode(string) tutorialrpc.PlayerServiceClient
	WhereLabels(map[string]string) tutorialrpc.PlayerServiceClient
	RouteRoundRobin() tutorialrpc.PlayerServiceClient
	RouteRandom() tutorialrpc.PlayerServiceClient
	Route(any) tutorialrpc.PlayerServiceClient
	RouteBy(rpc.RouteSelector) tutorialrpc.PlayerServiceClient
	IncludeRetired() tutorialrpc.PlayerServiceClient
	AwaitGetPlayer(context.Context, int64) (string, error)
	CallGetPlayer(context.Context, int64) (string, error)
	AsyncGetPlayer(context.Context, int64, func(context.Context, string, error)) error
	NotifyGetPlayer(context.Context, int64) error
	BroadcastGetPlayer(context.Context, int64) error
	NotifyRefresh(context.Context, int64) error
	BroadcastRefresh(context.Context, int64) error
}

// 以下断言只验证方法集和准确函数签名；具体默认值、状态和错误语义由所属包行为测试负责。
var (
	_ applicationFacade           = (*application.Application)(nil)
	_ serviceFacade               = (*service.Service)(nil)
	_ moduleFacade                = (*service.Module)(nil)
	_ commandRunnerFacade         = (*command.Runner)(nil)
	_ generatedPlayerClientFacade = tutorialrpc.PlayerServiceClient{}
	_ service.NodeRuntime         = (*node.Node)(nil)

	_ func(...application.Options) *application.Application = application.New
	_ func(command.Options) (*command.Runner, error)        = command.New
	_ func(string, any) error                               = config.LoadDir
	_ func(string) (*config.Snapshot, error)                = config.LoadSnapshot
	_ func(error) errs.Code                                 = errs.CodeOf
	_ func(error, errs.Code) bool                           = errs.IsCode

	_ func(string, admin.Handler, ...admin.Option) admin.Endpoint = admin.Get
	_ func(string, admin.Handler, ...admin.Option) admin.Endpoint = admin.Post
	_ func(int, any) (admin.Response, error)                      = admin.JSON
	_ func(int) admin.Response                                    = admin.Empty

	_ func(string, ...originlog.Field) = originlog.Debug
	_ func(string, ...originlog.Field) = originlog.Info
	_ func(string, ...originlog.Field) = originlog.Warn
	_ func(string, ...originlog.Field) = originlog.Error
	_ func(string, ...originlog.Field) = originlog.ErrorStack

	_ func(service.IService) tutorialrpc.PlayerServiceClient         = tutorialrpc.BindPlayerService
	_ func(service.IService, string) tutorialrpc.PlayerServiceClient = tutorialrpc.BindPlayerServiceTo
)

// 扩展 SPI 只固定最小接口形状；自定义实现仍由各自的行为测试和 providertest 套件验证。
type adminGuardFacade interface {
	Authorize(context.Context, *http.Request, admin.Operation) (admin.Principal, error)
}

type discoveryProviderFacade interface {
	Start(context.Context) error
	Publish(context.Context, publicprovider.Node) error
	Withdraw(context.Context) error
	Close(context.Context) error
}

type diagnosticsSourceFacade interface {
	Diagnostics() diagnostics.Snapshot
}

var (
	_ adminGuardFacade        = (admin.Guard)(nil)
	_ discoveryProviderFacade = (publicprovider.Provider)(nil)
	_ diagnosticsSourceFacade = (diagnostics.Source)(nil)
)
