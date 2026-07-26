package config

import "testing"

func FuzzDuration(f *testing.F) {
	// 种子同时提供合法、边界和典型非法时间文本。
	for _, seed := range []string{"0s", "15s", "2h30m", "", "-1s", "1s1m"} {
		f.Add(seed)
	}
	// 任意字符串都只能返回成功或错误，不能造成 panic。
	f.Fuzz(func(t *testing.T, input string) {
		var value Duration
		_ = value.UnmarshalText([]byte(input))
	})
}

func FuzzByteSize(f *testing.F) {
	// 种子覆盖合法单位和项目明确拒绝的常见别名。
	for _, seed := range []string{"0B", "16B", "4M", "", "-1M", "1MiB"} {
		f.Add(seed)
	}
	// 任意字符串都不能绕过范围检查或造成 panic。
	f.Fuzz(func(t *testing.T, input string) {
		var value ByteSize
		_ = value.UnmarshalText([]byte(input))
	})
}

func FuzzExpandString(f *testing.F) {
	// 种子覆盖普通文本、完整占位符、转义和不完整语法。
	for _, seed := range []string{"plain", "${NAME}", "$${NAME}", "${", "${1BAD}"} {
		f.Add(seed)
	}
	// 查询函数只定义 NAME，其他变量应走稳定错误路径。
	f.Fuzz(func(t *testing.T, input string) {
		_, _, _ = expandString(input, func(name string) (string, bool) {
			return "value", name == "NAME"
		})
	})
}

func FuzzParseData(f *testing.F) {
	// 同时提供 JSON/YAML 的合法和语法错误样本。
	f.Add("value: true\n", false)
	f.Add(`{"value":true}`, true)
	f.Add("value: [unterminated", false)
	f.Add(`{"value":}`, true)

	f.Fuzz(func(t *testing.T, input string, jsonFormat bool) {
		// 用布尔值选择扩展名，确保严格 JSON 分支也参与变异。
		format := ".yaml"
		if jsonFormat {
			format = ".json"
		}
		// 直接测试无文件系统解析阶段，关注任意输入不 panic。
		_, _ = parseData(configFile{relative: "fuzz" + format, format: format}, []byte(input))
	})
}

func FuzzParseAndMerge(f *testing.F) {
	// 种子覆盖独立 Mapping、Sequence 追加和 Scalar 冲突。
	f.Add("first: true\n", "second: false\n")
	f.Add("items: [one]\n", "items: [two]\n")
	f.Add("value: true\n", "value: false\n")

	f.Fuzz(func(t *testing.T, first, second string) {
		// 两份输入分别解析为独立 YAML 文档。
		left, leftErr := parseData(configFile{relative: "first.yaml", format: ".yaml"}, []byte(first))
		right, rightErr := parseData(configFile{relative: "second.yaml", format: ".yaml"}, []byte(second))
		// 只有双方都是合法根 Mapping 时才进入合并器。
		if leftErr == nil && rightErr == nil {
			_ = mergeNodes(left, right, "")
		}
	})
}
