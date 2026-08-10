package service

import (
	"strings"

	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
)

// IConfig 提供从合并后根配置读取显式路径的能力。
type IConfig interface {
	GetConfig(path string, destination any) error
}

// IServiceConfig 提供当前 Service 有效业务配置的读取能力。
type IServiceConfig interface {
	IConfig

	GetServiceConfig(path string, destination any) error
	ParseServiceConfig(destination any) error
}

// configRuntime 是业务配置快照能力的可选框架适配面。
//
// 它不加入 Runtime，避免扩展独立测试和第三方框架适配器必须实现的基础接口。
type configRuntime interface {
	RootConfig() originconfig.View
	ServiceConfig() originconfig.View
}

// GetConfig 从 Application 构建时冻结的根配置读取一个显式路径。
func (service *Service) GetConfig(path string, destination any) error {
	if err := service.configAccessError(); err != nil {
		return err
	}
	if !validConfigPath(path) {
		return errs.ErrInvalidArgument
	}
	view, err := service.configViews().root.Lookup(path)
	if err != nil {
		return err
	}
	return view.Decode(destination)
}

// GetServiceConfig 从当前 NodeID 与实际 ServiceName 选定的业务配置读取相对路径。
func (service *Service) GetServiceConfig(path string, destination any) error {
	if err := service.configAccessError(); err != nil {
		return err
	}
	if !validConfigPath(path) {
		return errs.ErrInvalidArgument
	}
	view, err := service.configViews().service.Lookup(path)
	if err != nil {
		return err
	}
	return view.Decode(destination)
}

// ParseServiceConfig 宽松解析当前 Service 的完整有效业务配置。
// 没有业务配置时仍验证 destination，并保留调用方的预填值。
func (service *Service) ParseServiceConfig(destination any) error {
	if err := service.configAccessError(); err != nil {
		return err
	}
	return service.configViews().service.Decode(destination)
}

type serviceConfigViews struct {
	root    originconfig.View
	service originconfig.View
}

func (service *Service) configViews() serviceConfigViews {
	runtime, ok := service.runtime.(configRuntime)
	if !ok {
		return serviceConfigViews{}
	}
	return serviceConfigViews{
		root:    runtime.RootConfig(),
		service: runtime.ServiceConfig(),
	}
}

func (service *Service) configAccessError() error {
	if service == nil || service.runtime == nil {
		return errs.ErrInvalidArgument
	}
	switch service.State() {
	case StateStopped:
		return errs.ErrServiceStopped
	case StateFailed:
		return errs.ErrServiceFailed
	default:
		return nil
	}
}

func validConfigPath(path string) bool {
	if path == "" || strings.ContainsAny(path, "*[]\\") {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			return false
		}
	}
	return true
}
