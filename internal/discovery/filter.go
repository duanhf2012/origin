package discovery

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/errs"
)

// Rule 是配置解码层交给发现内核的一条可选维度规则。
//
// 指针用来区分“未声明该维度”和“显式声明空值”。未声明维度不参与限制，显式空值属于
// 配置错误。
type Rule struct {
	Services   *[]string
	NodeLabels *map[string][]string
}

// compiledRule 保存启动冷路径预编译后的精确匹配集合。
type compiledRule struct {
	services   map[string]struct{}
	nodeLabels map[string]map[string]struct{}
}

// Filter 是一个 Node 生命周期内冻结的关注规则。
//
// configured=false 表示配置字段完全省略，应允许全部公开远端 Service；configured=true 且
// rules 为空表示显式拒绝全部。匹配热路径只执行 Map 查询，不解析文本或运行正则。
type Filter struct {
	configured bool
	rules      []compiledRule
}

// CompileFilter 校验并预编译一组 allow_discovery 规则。
func CompileFilter(configured bool, rules []Rule) (Filter, error) {
	// 字段完全省略时直接建立允许全部的紧凑零规则结果。
	if !configured {
		if len(rules) != 0 {
			return Filter{}, invalidConfig("未配置 allow_discovery 时不能携带规则")
		}
		return Filter{}, nil
	}

	// 显式空列表保留 configured 标记，Match 会稳定拒绝全部目标。
	result := Filter{
		configured: true,
		rules:      make([]compiledRule, 0, len(rules)),
	}
	for index, source := range rules {
		// 两个维度均未声明的规则会无条件放行全部，容易把配置错误静默扩大为全量发现。
		if source.Services == nil && source.NodeLabels == nil {
			return Filter{}, invalidConfig(
				fmt.Sprintf("allow_discovery[%d] 不能是空规则", index),
			)
		}
		compiled := compiledRule{}

		// Service 维度在冷路径转换为集合，重复名称自然合并，不增加匹配分支。
		if source.Services != nil {
			if len(*source.Services) == 0 {
				return Filter{}, invalidConfig(
					fmt.Sprintf("allow_discovery[%d].services 不能为空", index),
				)
			}
			compiled.services = make(map[string]struct{}, len(*source.Services))
			for serviceIndex, name := range *source.Services {
				if name == "" {
					return Filter{}, invalidConfig(fmt.Sprintf(
						"allow_discovery[%d].services[%d] 不能为空",
						index,
						serviceIndex,
					))
				}
				compiled.services[name] = struct{}{}
			}
		}

		// 标签维度使用两层集合表达“不同键 AND、同一键多个值 OR”。
		if source.NodeLabels != nil {
			if len(*source.NodeLabels) == 0 {
				return Filter{}, invalidConfig(
					fmt.Sprintf("allow_discovery[%d].node_labels 不能为空", index),
				)
			}
			compiled.nodeLabels = make(
				map[string]map[string]struct{},
				len(*source.NodeLabels),
			)
			for key, values := range *source.NodeLabels {
				if key == "" || len(values) == 0 {
					return Filter{}, invalidConfig(fmt.Sprintf(
						"allow_discovery[%d].node_labels 的键和值不能为空",
						index,
					))
				}
				valueSet := make(map[string]struct{}, len(values))
				for _, value := range values {
					if value == "" {
						return Filter{}, invalidConfig(fmt.Sprintf(
							"allow_discovery[%d].node_labels[%q] 包含空值",
							index,
							key,
						))
					}
					valueSet[value] = struct{}{}
				}
				compiled.nodeLabels[key] = valueSet
			}
		}
		result.rules = append(result.rules, compiled)
	}
	return result, nil
}

// Match 报告指定公开 Service 是否通过当前 Node 的关注规则。
func (filter Filter) Match(node RawNode, service RawService) bool {
	// 未配置采用关注全部的当前默认值；显式空规则列表则自然没有任何匹配项。
	if !filter.configured {
		return true
	}
	for _, rule := range filter.rules {
		// 声明了 Service 维度时，名称必须命中同一规则的集合。
		if rule.services != nil {
			if _, exists := rule.services[service.ServiceName]; !exists {
				continue
			}
		}

		// 声明了标签维度时，每一个键都必须存在且命中其允许值集合。
		labelsMatched := true
		for key, values := range rule.nodeLabels {
			value, exists := node.Labels[key]
			if !exists {
				labelsMatched = false
				break
			}
			if _, exists = values[value]; !exists {
				labelsMatched = false
				break
			}
		}
		if labelsMatched {
			return true
		}
	}
	return false
}

// invalidConfig 为筛选冷路径附加稳定配置错误码。
func invalidConfig(message string) error {
	return errs.NewMessage(errs.CodeInvalidConfig, message)
}
