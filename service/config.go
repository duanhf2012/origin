package service

import (
	originconfig "github.com/duanhf2012/origin/v3/config"
	"github.com/duanhf2012/origin/v3/errs"
)

// configRuntime 是 M21 配置能力的可选框架适配面。
//
// 它不加入 Runtime，避免扩张独立测试和第三方框架适配器必须实现的基础接口。
type configRuntime interface {
	RootConfig() originconfig.View
	ServiceConfig() originconfig.View
}

// Config 返回按当前 NodeID 和实际 ServiceName 选定的冻结业务配置视图。
//
// 没有配置时返回零值 View；该视图可安全复制并发读取。
func (service *Service) Config() originconfig.View {
	if service == nil || service.runtime == nil {
		return originconfig.View{}
	}
	runtime, ok := service.runtime.(configRuntime)
	if !ok {
		return originconfig.View{}
	}
	return runtime.ServiceConfig()
}

// DecodeConfig 把当前 Service 的有效配置宽松解码到 destination。
//
// 没有配置时只验证 destination 并保持其预填值不变。
func (service *Service) DecodeConfig(destination any) error {
	return service.Config().Decode(destination)
}

// DecodeConfigAt 把当前 Service 配置下的显式相对路径解码到 destination。
//
// 与 DecodeConfig 的“缺失即默认”不同，显式路径不存在时返回 ErrConfigNotFound。
func (service *Service) DecodeConfigAt(path string, destination any) error {
	if service == nil || path == "" {
		return errs.ErrInvalidArgument
	}
	view, err := service.Config().Lookup(path)
	if err != nil {
		return err
	}
	return view.Decode(destination)
}
