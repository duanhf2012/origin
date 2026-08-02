package rpc

import (
	"fmt"
	"sync"
)

// GeneratedContractDescriptor 描述 origingen 在契约包中生成的服务端装配信息。
//
// 描述符只在 Node 冷启动时按 Service 模板名查询。成功创建的 Dispatcher 仍沿用
// 原有静态调用路径，RPC 热路径不会访问该注册表，也不会使用反射。
type GeneratedContractDescriptor struct {
	ServiceName   string
	ContractName  string
	ContractID    ContractID
	Fingerprint   ContractFingerprint
	NewDispatcher func(implementation any) (Dispatcher, bool)
}

type generatedContractRegistry struct {
	mu        sync.RWMutex
	byService map[string]GeneratedContractDescriptor
	err       error
}

var generatedContracts generatedContractRegistry

// RegisterGeneratedContract 注册一份由 origingen 生成的不可变契约描述符。
//
// 该函数供生成代码的 init 调用。无效或冲突的注册不会 panic，而会由 Node.New 在
// 冷启动装配时返回明确错误。
func RegisterGeneratedContract(descriptor GeneratedContractDescriptor) {
	generatedContracts.register(descriptor)
}

// FindGeneratedContract 按原始 Service 模板名查找生成契约。
//
// serviceName 必须是配置 actual:Template 中的 Template，而不是改名后的 actual。
func FindGeneratedContract(
	serviceName string,
) (GeneratedContractDescriptor, bool, error) {
	return generatedContracts.find(serviceName)
}

func (registry *generatedContractRegistry) register(
	descriptor GeneratedContractDescriptor,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	if registry.err != nil {
		return
	}
	if err := validateGeneratedContract(descriptor); err != nil {
		registry.err = err
		return
	}
	if registry.byService == nil {
		registry.byService = make(map[string]GeneratedContractDescriptor)
	}
	previous, exists := registry.byService[descriptor.ServiceName]
	if !exists {
		registry.byService[descriptor.ServiceName] = descriptor
		return
	}
	// 同一个生成包被工具链以等价路径重复加载时允许幂等注册；函数值不能比较，
	// 因而使用完整的稳定身份判断是否为同一份契约。
	if previous.ContractName == descriptor.ContractName &&
		previous.ContractID == descriptor.ContractID &&
		previous.Fingerprint == descriptor.Fingerprint {
		return
	}
	registry.err = fmt.Errorf(
		"RPC Service 模板名 %q 同时关联契约 %q 和 %q",
		descriptor.ServiceName,
		previous.ContractName,
		descriptor.ContractName,
	)
}

func (registry *generatedContractRegistry) find(
	serviceName string,
) (GeneratedContractDescriptor, bool, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if registry.err != nil {
		return GeneratedContractDescriptor{}, false, registry.err
	}
	descriptor, found := registry.byService[serviceName]
	return descriptor, found, nil
}

func validateGeneratedContract(descriptor GeneratedContractDescriptor) error {
	switch {
	case descriptor.ServiceName == "":
		return fmt.Errorf("生成 RPC 契约缺少 ServiceName")
	case descriptor.ContractName == "":
		return fmt.Errorf("RPC Service 模板 %q 缺少 ContractName", descriptor.ServiceName)
	case descriptor.ContractID == 0:
		return fmt.Errorf("RPC 契约 %q 的 ContractID 不能为零", descriptor.ContractName)
	case descriptor.NewDispatcher == nil:
		return fmt.Errorf("RPC 契约 %q 缺少 Dispatcher 工厂", descriptor.ContractName)
	default:
		return nil
	}
}
