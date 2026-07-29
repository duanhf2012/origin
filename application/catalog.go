package application

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/service"
)

// serviceTemplate 保存一类 Service 的名称和可重复实例化的指针类型。
type serviceTemplate struct {
	name        string
	pointerType reflect.Type
}

// serviceCatalog 是 Application 私有的模板注册表。
type serviceCatalog struct {
	mu        sync.Mutex
	templates map[string]serviceTemplate
	frozen    bool
	err       error
}

// recordError 保存目录外观层发现的首个错误。
func (catalog *serviceCatalog) recordError(err error) {
	if err == nil {
		return
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err == nil {
		catalog.err = err
	}
}

// setup 注册零值 Service 样本；所有错误延迟到 Start 统一返回。
func (catalog *serviceCatalog) setup(samples ...service.IService) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()

	// 首个错误足以阻止启动，保留它可让诊断稳定且避免后续覆盖根因。
	if catalog.err != nil {
		return
	}
	if catalog.frozen {
		catalog.err = errs.NewMessage(
			errs.CodeInvalidArgument,
			"Application 启动后不能继续 Setup Service",
		)
		return
	}
	if len(samples) == 0 {
		catalog.err = errs.NewMessage(
			errs.CodeInvalidArgument,
			"Setup 至少需要一个 Service 模板",
		)
		return
	}
	if catalog.templates == nil {
		catalog.templates = make(map[string]serviceTemplate, len(samples))
	}

	// 每个样本只用于确定类型，运行实例始终由 reflect.New 创建。
	for _, sample := range samples {
		template, err := inspectTemplate(sample)
		if err != nil {
			catalog.err = err
			return
		}
		if template.name == "DiscoveryService" {
			catalog.err = errs.NewMessage(
				errs.CodeInvalidArgument,
				"DiscoveryService 是框架保留名称，业务不能注册同名 Service",
			)
			return
		}
		if registered, exists := catalog.templates[template.name]; exists {
			// 同一 Go 类型的重复 Setup 按 v2 使用习惯保持幂等。
			if registered.pointerType == template.pointerType {
				continue
			}
			catalog.err = errs.NewMessage(
				errs.CodeInvalidArgument,
				fmt.Sprintf("Service 模板名 %q 已由其他 Go 类型注册", template.name),
			)
			return
		}
		catalog.templates[template.name] = template
	}
}

// freeze 在生命周期开始前冻结注册表，并返回此前积累的 Setup 错误。
func (catalog *serviceCatalog) freeze() error {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.frozen = true
	return catalog.err
}

// instantiate 创建一个完全独立且仍处于零值状态的 Service 实例。
func (catalog *serviceCatalog) instantiate(name string) (service.IService, error) {
	catalog.mu.Lock()
	template, exists := catalog.templates[name]
	catalog.mu.Unlock()
	if !exists {
		return nil, errs.NewMessage(
			errs.CodeInvalidConfig,
			fmt.Sprintf("Service 模板 %q 尚未通过 app.Setup 注册", name),
		)
	}

	// pointerType.Elem 是已验证的命名结构体；新指针不会共享样本状态。
	instance := reflect.New(template.pointerType.Elem()).Interface()
	target, ok := instance.(service.IService)
	if !ok {
		// 该分支意味着注册后类型信息被破坏，按框架内部错误处理。
		return nil, errs.NewMessage(
			errs.CodeInternal,
			fmt.Sprintf("Service 模板 %q 无法实例化", name),
		)
	}
	return target, nil
}

// inspectTemplate 校验样本满足“指向命名结构体的零值指针”约束。
func inspectTemplate(sample service.IService) (serviceTemplate, error) {
	if sample == nil {
		return serviceTemplate{}, errs.NewMessage(
			errs.CodeInvalidArgument,
			"Service 模板不能为空",
		)
	}
	value := reflect.ValueOf(sample)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return serviceTemplate{}, errs.NewMessage(
			errs.CodeInvalidArgument,
			"Service 模板必须是非空结构体指针",
		)
	}
	elementType := value.Type().Elem()
	if elementType.Kind() != reflect.Struct || elementType.Name() == "" {
		return serviceTemplate{}, errs.NewMessage(
			errs.CodeInvalidArgument,
			"Service 模板必须指向具名结构体",
		)
	}
	if !value.Elem().IsZero() {
		return serviceTemplate{}, errs.NewMessage(
			errs.CodeInvalidArgument,
			fmt.Sprintf("Service 模板 %q 必须是零值样本", elementType.Name()),
		)
	}
	return serviceTemplate{
		name:        elementType.Name(),
		pointerType: value.Type(),
	}, nil
}
