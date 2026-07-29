package provider

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/duanhf2012/origin/v3/errs"
)

// Config 保存当前被选择 Provider 的只读严格配置。
//
// 内部 JSON 快照不向 Provider 暴露；第三方只能把它解码到自己的强类型结构。
type Config struct {
	data []byte
}

// NewConfig 为框架集成层冻结一个 Provider 配置块。
//
// 业务项目通常不需要直接调用；公开构造只用于 Application 与外部契约测试。
func NewConfig(value any) (Config, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Config{}, errs.Wrap(errs.CodeInvalidConfig, err)
	}
	return Config{data: append([]byte(nil), data...)}, nil
}

// Decode 严格解码当前配置块，拒绝未知字段、非指针和尾随数据。
func (config Config) Decode(destination any) error {
	if destination == nil {
		return errs.NewMessage(errs.CodeInvalidArgument, "Provider 配置目标不能为空")
	}
	decoder := json.NewDecoder(bytes.NewReader(config.data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errs.Wrap(errs.CodeInvalidConfig, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errs.NewMessage(errs.CodeInvalidConfig, "Provider 配置包含尾随数据")
	}
	return nil
}
