package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// testText 是测试 encoding.TextUnmarshaler 优先级的自定义配置类型。
type testText string

func (value *testText) UnmarshalText(text []byte) error {
	// 测试类型只接受 text: 前缀，用于确认自定义文本解码和错误传播。
	if !strings.HasPrefix(string(text), "text:") {
		return errors.New("缺少 text: 前缀")
	}
	// 合法输入去掉前缀后保存，便于断言接口确实被调用。
	*value = testText(strings.TrimPrefix(string(text), "text:"))
	return nil
}

func TestLoadDirMixedFiles(t *testing.T) {
	// 设置字符串和基础类型所需环境变量，值只在当前测试进程范围生效。
	t.Setenv("ORIGIN_TEST_HOST", "127.0.0.1")
	t.Setenv("ORIGIN_TEST_PORT", "7201")
	t.Setenv("ORIGIN_TEST_ENABLED", "true")
	t.Setenv("ORIGIN_TEST_RATIO", "1.25")

	// 故意混合 JSON、嵌套 YAML、Sequence、Map、转义和自定义文本字段。
	dir := t.TempDir()
	writeConfig(t, dir, "20-nodes.json", `{
  "nodes": [{"id": "game-1"}],
  "dynamic": {"2": "two"}
}`)
	writeConfig(t, dir, "nested/10-base.yaml", `
server_info:
  name: origin
  default_timeout: 15s
  max_message_size: 4M
  address: "tcp://${ORIGIN_TEST_HOST}:${ORIGIN_TEST_PORT}"
  port: "${ORIGIN_TEST_PORT}"
  enabled: "${ORIGIN_TEST_ENABLED}"
  ratio: "${ORIGIN_TEST_RATIO}"
  literal: "$${KEEP}"
  custom: "text:decoded"
  labels:
    region: cn-east
nodes:
  - id: gateway-1
dynamic:
  "1": one
actual_field: yaml-tag-is-ignored
`)

	// 目标模型覆盖匿名嵌入、yaml Tag 忽略和所有主要强类型。
	type Embedded struct {
		ActualField string `yaml:"wrong_name"`
	}
	type serverInfo struct {
		Name           string
		DefaultTimeout Duration
		MaxMessageSize ByteSize
		Address        string
		Port           uint16
		Enabled        bool
		Ratio          float64
		Literal        string
		Custom         testText
		Labels         map[string]string
	}
	type node struct {
		ID string
	}
	type target struct {
		Embedded
		ServerInfo serverInfo
		Nodes      []node
		Dynamic    map[int]string
	}

	// 执行唯一公开加载入口。
	var got target
	if err := LoadDir(dir, &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}

	// 分组断言自动字段映射、严格单位、环境转换和自定义文本结果。
	if got.ActualField != "yaml-tag-is-ignored" {
		t.Fatalf("ActualField = %q", got.ActualField)
	}
	if got.ServerInfo.Name != "origin" ||
		got.ServerInfo.DefaultTimeout.Duration() != 15*time.Second ||
		got.ServerInfo.MaxMessageSize.Bytes() != 4<<20 ||
		got.ServerInfo.Address != "tcp://127.0.0.1:7201" ||
		got.ServerInfo.Port != 7201 ||
		!got.ServerInfo.Enabled ||
		got.ServerInfo.Ratio != 1.25 ||
		got.ServerInfo.Literal != "${KEEP}" ||
		got.ServerInfo.Custom != "decoded" {
		t.Fatalf("ServerInfo 解码结果不正确: %+v", got.ServerInfo)
	}
	// Map 和 Sequence 应按稳定文件顺序组合。
	if !reflect.DeepEqual(got.ServerInfo.Labels, map[string]string{"region": "cn-east"}) {
		t.Fatalf("Labels = %#v", got.ServerInfo.Labels)
	}
	if !reflect.DeepEqual(got.Nodes, []node{{ID: "game-1"}, {ID: "gateway-1"}}) {
		t.Fatalf("Nodes = %#v", got.Nodes)
	}
	if !reflect.DeepEqual(got.Dynamic, map[int]string{1: "one", 2: "two"}) {
		t.Fatalf("Dynamic = %#v", got.Dynamic)
	}
}

func TestLoadDirRecursivelyMergesMappingsAndSequences(t *testing.T) {
	// 在不同目录和格式中拆分同一 Mapping 与 Sequence。
	dir := t.TempDir()
	// 故意逆序创建文件，验证结果只由规范化相对路径决定。
	writeConfig(t, dir, "z/30-third.yml", "items: [third]\n")
	writeConfig(t, dir, "10-first.yaml", "nested:\n  first: 1\nitems: [first]\n")
	writeConfig(t, dir, "20-second.json", `{"nested":{"second":2},"items":["second"]}`)
	writeConfig(t, dir, "ignored.toml", "items = ['ignored']")

	// 加载后验证 Mapping 递归补充、Sequence 追加以及不支持格式忽略。
	var got struct {
		Nested map[string]int
		Items  []string
	}
	if err := LoadDir(dir, &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}
	if !reflect.DeepEqual(got.Nested, map[string]int{"first": 1, "second": 2}) {
		t.Fatalf("Nested = %#v", got.Nested)
	}
	if !reflect.DeepEqual(got.Items, []string{"first", "second", "third"}) {
		t.Fatalf("Items = %#v", got.Items)
	}
}

func TestLoadDirTestdata(t *testing.T) {
	t.Parallel()

	// 使用仓库静态样本验证测试运行目录下的真实相对路径加载。
	var got struct {
		Framework struct {
			Name string
		}
		Nodes []struct {
			ID string
		}
	}
	// 执行加载并断言两个文件按名称顺序合并。
	if err := LoadDir("testdata/merged", &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}
	if got.Framework.Name != "origin-v3" ||
		len(got.Nodes) != 2 ||
		got.Nodes[0].ID != "gateway-1" ||
		got.Nodes[1].ID != "game-1" {
		t.Fatalf("testdata 合并结果错误: %+v", got)
	}
}

func TestLoadDirPreservesDefaultsAndDestinationOnFailure(t *testing.T) {
	// 嵌套指针和 Map 用于验证默认值所有权不会被反射解码污染。
	type nested struct {
		Name string
	}
	type target struct {
		ANested *nested
		Values  map[string]int
	}

	t.Run("preserve defaults", func(t *testing.T) {
		// 配置只修改嵌套指针，Map 字段完全缺失。
		dir := t.TempDir()
		writeConfig(t, dir, "config.yaml", "a_nested:\n  name: changed\n")

		originalNested := &nested{Name: "default"}
		// 预初始化目标默认值，再执行 LoadDir 覆盖出现字段。
		got := target{
			ANested: originalNested,
			Values:  map[string]int{"default": 1},
		}
		if err := LoadDir(dir, &got); err != nil {
			t.Fatalf("LoadDir 返回错误: %v", err)
		}
		// 新指针必须与原默认对象分离，缺失 Map 必须原样保留。
		if got.ANested == originalNested {
			t.Fatal("出现配置的指针字段应复制后更新，不能复用默认对象")
		}
		if got.ANested.Name != "changed" || originalNested.Name != "default" {
			t.Fatalf("指针默认值被污染: got=%+v original=%+v", got.ANested, originalNested)
		}
		if !reflect.DeepEqual(got.Values, map[string]int{"default": 1}) {
			t.Fatalf("缺失字段的默认 Map 未保留: %#v", got.Values)
		}
	})

	t.Run("failure is atomic", func(t *testing.T) {
		// 在两个合法修改之后加入未知字段，迫使解码后段失败。
		dir := t.TempDir()
		writeConfig(t, dir, "config.yaml", `
a_nested:
  name: changed
values:
  changed: 2
z_unknown: true
`)

		originalNested := &nested{Name: "default"}
		originalMap := map[string]int{"default": 1}
		// 保存原始引用，失败后验证指针内容和 Map 都未提交。
		got := target{ANested: originalNested, Values: originalMap}
		err := LoadDir(dir, &got)
		if err == nil {
			t.Fatal("未知字段应返回错误")
		}
		if got.ANested != originalNested || got.ANested.Name != "default" {
			t.Fatalf("失败后指针字段被修改: %+v", got.ANested)
		}
		if !reflect.DeepEqual(got.Values, map[string]int{"default": 1}) {
			t.Fatalf("失败后 Map 被修改: %#v", got.Values)
		}
	})
}

func TestLoadDirStrictErrors(t *testing.T) {
	// 表格覆盖未知字段、类型、溢出、严格 JSON 和 YAML 文档边界。
	tests := []struct {
		name     string
		filename string
		content  string
		contains []string
	}{
		{
			name:     "unknown field",
			filename: "config.yaml",
			content:  "known: ok\nunknown: value\n",
			contains: []string{"config.yaml:2:", `未知配置字段 "unknown"`},
		},
		{
			name:     "type mismatch",
			filename: "config.yaml",
			content:  "known: 12\n",
			contains: []string{"config.yaml:1:", "不能解码到 string"},
		},
		{
			name:     "integer overflow",
			filename: "config.yaml",
			content:  "small: 128\n",
			contains: []string{"config.yaml:1:", "int8", "溢出"},
		},
		{
			name:     "strict json comment",
			filename: "config.json",
			content:  "{\n// comment\n\"known\":\"ok\"\n}",
			contains: []string{"config.json", "严格 JSON"},
		},
		{
			name:     "strict json trailing comma",
			filename: "config.json",
			content:  `{"known":"ok",}`,
			contains: []string{"config.json", "严格 JSON"},
		},
		{
			name:     "yaml multiple documents",
			filename: "config.yaml",
			content:  "known: first\n---\nknown: second\n",
			contains: []string{"config.yaml", "多文档"},
		},
		{
			name:     "root sequence",
			filename: "config.yaml",
			content:  "- one\n- two\n",
			contains: []string{"config.yaml:1:", "根节点必须是 Mapping"},
		},
		{
			name:     "root scalar",
			filename: "config.yaml",
			content:  "value\n",
			contains: []string{"config.yaml:1:", "根节点必须是 Mapping"},
		},
		{
			name:     "empty document",
			filename: "config.yaml",
			content:  "# only a comment\n",
			contains: []string{"config.yaml", "不能为空"},
		},
		{
			name:     "yaml syntax",
			filename: "config.yaml",
			content:  "known: [unterminated\n",
			contains: []string{"config.yaml"},
		},
		{
			name:     "duplicate yaml key",
			filename: "config.yaml",
			content:  "known: first\nknown: second\n",
			contains: []string{"config.yaml", "known"},
		},
		{
			name:     "non string yaml key",
			filename: "config.yaml",
			content:  "1: value\n",
			contains: []string{"config.yaml"},
		},
	}

	// 每个样本独占目录，并统一解码到具有已知字段的目标。
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, test.filename, test.content)
			var got struct {
				Known string
				Small int8
			}
			err := LoadDir(dir, &got)
			// 所有样本必须返回稳定配置错误码。
			if err == nil {
				t.Fatal("LoadDir 应返回错误")
			}
			if !errs.IsCode(err, errs.CodeInvalidConfig) {
				t.Fatalf("错误码 = %d，期望 CodeInvalidConfig: %v", errs.CodeOf(err), err)
			}
			// 同时检查关键文件、行列或错误原因片段。
			for _, expected := range test.contains {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("错误 %q 不包含 %q", err, expected)
				}
			}
		})
	}
}

func TestLoadDirAcceptsSupportedExtensionCase(t *testing.T) {
	// 使用三种支持扩展名的大写形式建立配置片段。
	dir := t.TempDir()
	writeConfig(t, dir, "10-first.JSON", `{"items":["json"]}`)
	writeConfig(t, dir, "20-second.YML", "items: [yml]\n")
	writeConfig(t, dir, "30-third.YAML", "items: [yaml]\n")

	// 加载后验证扩展名匹配不区分大小写且排序保持稳定。
	var got struct{ Items []string }
	if err := LoadDir(dir, &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}
	if !reflect.DeepEqual(got.Items, []string{"json", "yml", "yaml"}) {
		t.Fatalf("Items = %#v", got.Items)
	}
}

func TestLoadDirRejectsCaseFoldedPathCollision(t *testing.T) {
	// 在大小写敏感文件系统上建立只差大小写的两个逻辑路径。
	dir := t.TempDir()
	writeConfig(t, dir, "Case.yaml", "first: true\n")
	writeConfig(t, dir, "case.yaml", "second: true\n")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		// Windows 等大小写不敏感文件系统无法构造此边界，显式跳过。
		t.Skip("当前文件系统大小写不敏感，不能建立冲突样本")
	}

	// 扫描阶段必须拒绝，防止配置跨平台得到不同结果。
	var got map[string]any
	err = LoadDir(dir, &got)
	if err == nil || !strings.Contains(err.Error(), "忽略大小写后冲突") {
		t.Fatalf("大小写折叠路径冲突应返回错误: %v", err)
	}
}

func TestLoadDirRejectsCrossFileConflicts(t *testing.T) {
	// 分别覆盖 Scalar 重复、Null 重复和容器类型冲突。
	tests := []struct {
		name   string
		first  string
		second string
	}{
		{name: "scalar", first: "value: first\n", second: "value: second\n"},
		{name: "null", first: "value: null\n", second: "value: null\n"},
		{name: "node type", first: "value:\n  child: true\n", second: "value: []\n"},
	}
	// 每个冲突由两个有序文件组成。
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, "10-first.yaml", test.first)
			writeConfig(t, dir, "20-second.yaml", test.second)
			var got map[string]any
			err := LoadDir(dir, &got)
			if err == nil {
				t.Fatal("重复或冲突配置应返回错误")
			}
			// 错误必须同时指出首次、再次来源和逻辑配置路径。
			for _, expected := range []string{"10-first.yaml:", "20-second.yaml:1:", `配置路径 "value"`} {
				if !strings.Contains(err.Error(), expected) {
					t.Errorf("错误 %q 不包含 %q", err, expected)
				}
			}
		})
	}
}

func TestLoadDirEnvironmentErrorsDoNotLeakValues(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		// 未定义变量应报告变量名和来源位置。
		dir := t.TempDir()
		writeConfig(t, dir, "config.yaml", "value: ${ORIGIN_M3_MISSING}\n")
		var got struct{ Value string }
		err := LoadDir(dir, &got)
		if err == nil || !strings.Contains(err.Error(), "ORIGIN_M3_MISSING") ||
			!strings.Contains(err.Error(), "config.yaml:1:") {
			t.Fatalf("缺失环境变量错误不完整: %v", err)
		}
	})

	t.Run("conversion", func(t *testing.T) {
		// 使用明显秘密文本触发 int 转换失败，检查错误脱敏。
		const secret = "secret-that-must-not-leak"
		t.Setenv("ORIGIN_M3_SECRET", secret)
		dir := t.TempDir()
		writeConfig(t, dir, "config.yaml", "value: ${ORIGIN_M3_SECRET}\n")
		var got struct{ Value int }
		err := LoadDir(dir, &got)
		if err == nil {
			t.Fatal("非法整数环境变量应返回错误")
		}
		// 错误不得包含环境变量实际值。
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("错误泄露了环境变量值: %v", err)
		}
	})
}

func TestLoadDirInvalidArgumentsAndDirectories(t *testing.T) {
	// 建立有类型 nil 指针和普通文件路径两类特殊输入。
	var nilTarget *struct{}
	file := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 表格区分调用参数错误和配置来源错误的稳定错误码。
	tests := []struct {
		name string
		dir  string
		dst  any
		code errs.Code
	}{
		{name: "empty directory argument", dir: "", dst: &struct{}{}, code: errs.CodeInvalidArgument},
		{name: "non pointer", dir: t.TempDir(), dst: struct{}{}, code: errs.CodeInvalidArgument},
		{name: "nil target", dir: t.TempDir(), dst: nil, code: errs.CodeInvalidArgument},
		{name: "nil pointer", dir: t.TempDir(), dst: nilTarget, code: errs.CodeInvalidArgument},
		{name: "file as directory", dir: file, dst: &struct{}{}, code: errs.CodeInvalidArgument},
		{name: "missing directory", dir: filepath.Join(t.TempDir(), "missing"), dst: &struct{}{}, code: errs.CodeInvalidConfig},
		{name: "empty config directory", dir: t.TempDir(), dst: &struct{}{}, code: errs.CodeInvalidConfig},
	}
	// 每个样本只检查分类，具体错误文本由其他测试覆盖。
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			err := LoadDir(test.dir, test.dst)
			if err == nil || !errs.IsCode(err, test.code) {
				t.Fatalf("LoadDir 错误 = %v，期望错误码 %d", err, test.code)
			}
		})
	}
}

func TestLoadDirConcurrent(t *testing.T) {
	// 建立只读配置目录供多个 goroutine 同时加载。
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "value: 42\n")

	const workers = 16
	// 每个 worker 使用独立目标，错误集中到有界通道。
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var got struct{ Value int }
			if err := LoadDir(dir, &got); err != nil {
				errorsFound <- err
				return
			}
			if got.Value != 42 {
				errorsFound <- errors.New("并发加载结果错误")
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	// 全部 worker 完成后统一报告加载或数据错误。
	for err := range errorsFound {
		t.Error(err)
	}
}

func TestLoadDirSymlinkBoundary(t *testing.T) {
	// 根目录内和根目录外各建立一个真实配置文件。
	root := t.TempDir()
	writeConfig(t, root, "inside.yaml", "inside: true\n")
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.yaml")
	if err := os.WriteFile(outside, []byte("outside: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 首先建立指向根内文件的链接；平台不支持时跳过全部链接断言。
	insideLink := filepath.Join(root, "inside-link.yaml")
	if err := os.Symlink(filepath.Join(root, "inside.yaml"), insideLink); err != nil {
		t.Skipf("当前平台不能创建符号链接: %v", err)
	}
	// 同一内容通过链接再次出现会触发重复定义，这也证明根目录内的文件链接被读取。
	var got map[string]any
	err := LoadDir(root, &got)
	if err == nil || !strings.Contains(err.Error(), "inside-link.yaml") {
		t.Fatalf("根目录内文件链接未按配置片段读取: %v", err)
	}

	if err := os.Remove(insideLink); err != nil {
		t.Fatal(err)
	}
	// 替换为越界链接，扫描必须在读取内容前拒绝。
	outsideLink := filepath.Join(root, "outside-link.yaml")
	if err := os.Symlink(outside, outsideLink); err != nil {
		t.Fatal(err)
	}
	err = LoadDir(root, &got)
	if err == nil || !strings.Contains(err.Error(), "越出配置目录") {
		t.Fatalf("越界文件链接应被拒绝: %v", err)
	}

	if err := os.Remove(outsideLink); err != nil {
		t.Fatal(err)
	}
	// 最后建立失效链接，验证 EvalSymlinks 错误不会被忽略。
	brokenLink := filepath.Join(root, "broken.yaml")
	if err := os.Symlink(filepath.Join(root, "missing.yaml"), brokenLink); err != nil {
		t.Fatal(err)
	}
	err = LoadDir(root, &got)
	if err == nil || !strings.Contains(err.Error(), "无法解析配置文件链接") {
		t.Fatalf("失效文件链接应被拒绝: %v", err)
	}
}

func TestLoadDirDoesNotFollowSymlinkDirectory(t *testing.T) {
	// 根内和外部目录各放一份配置，外部通过目录链接暴露。
	root := t.TempDir()
	writeConfig(t, root, "inside.yaml", "inside: true\n")
	outside := t.TempDir()
	writeConfig(t, outside, "outside.yaml", "outside: true\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked-directory")); err != nil {
		t.Skipf("当前平台不能创建符号链接: %v", err)
	}

	// 加载目标不声明 outside 字段；成功即证明未递归进入链接目录。
	var got struct{ Inside bool }
	if err := LoadDir(root, &got); err != nil {
		t.Fatalf("符号链接目录应被忽略: %v", err)
	}
	if !got.Inside {
		t.Fatal("根目录内普通配置未加载")
	}
}

func TestExpandString(t *testing.T) {
	t.Parallel()

	// 使用确定性查找函数隔离进程环境。
	lookup := func(name string) (string, bool) {
		values := map[string]string{
			"A": "alpha",
			"B": "beta",
		}
		value, exists := values[name]
		return value, exists
	}
	// 样本覆盖普通美元、完整占位、多个占位和转义。
	tests := []struct {
		input string
		want  string
		exact bool
	}{
		{input: "plain", want: "plain"},
		{input: "$A", want: "$A"},
		{input: "${A}", want: "alpha", exact: true},
		{input: "${A}-${B}", want: "alpha-beta"},
		{input: "$${A}", want: "${A}"},
		{input: "x$${A}y", want: "x${A}y"},
	}
	// 同时验证展开文本与 exact 类型转换标记。
	for _, test := range tests {
		got, exact, err := expandString(test.input, lookup)
		if err != nil {
			t.Errorf("expandString(%q) 返回错误: %v", test.input, err)
			continue
		}
		if got != test.want || exact != test.exact {
			t.Errorf("expandString(%q) = (%q, %v)，期望 (%q, %v)", test.input, got, exact, test.want, test.exact)
		}
	}
	// 不完整、非法或未定义变量都必须返回错误。
	for _, input := range []string{"${}", "${1A}", "${MISSING}", "${A"} {
		if _, _, err := expandString(input, lookup); err == nil {
			t.Errorf("expandString(%q) 应返回错误", input)
		}
	}
}

func TestDecodeMapKeyAndInterface(t *testing.T) {
	// 配置包含 bool/uint Map Key 和任意值树。
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", `
bools:
  true: yes
uints:
  "2": two
anything:
  list: [1, true, value]
`)
	// 目标类型触发基础 Map Key 转换和空接口恢复。
	var got struct {
		Bools    map[bool]string
		Uints    map[uint8]string
		Anything any
	}
	if err := LoadDir(dir, &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}
	if got.Bools[true] != "yes" || got.Uints[2] != "two" {
		t.Fatalf("Map Key 解码错误: bools=%#v uints=%#v", got.Bools, got.Uints)
	}
	// 空接口 Mapping 应恢复为 map[string]any。
	anything, ok := got.Anything.(map[string]any)
	if !ok || len(anything) != 1 {
		t.Fatalf("空接口解码错误: %#v", got.Anything)
	}
}

func TestCollectStructFieldsRejectsRecursiveEmbedding(t *testing.T) {
	t.Parallel()

	// 匿名指针嵌入自身形成反射 DFS 环。
	type Recursive struct {
		*Recursive
	}
	// 字段模型收集必须返回错误而不是无限递归。
	fields := collectStructFields(reflect.TypeFor[Recursive]())
	if fields.err == nil {
		t.Fatal("递归匿名嵌入应返回配置模型错误")
	}
}

func writeConfig(t *testing.T, root, relative, content string) {
	t.Helper()
	// 把斜杠样本路径转换为当前平台路径并创建父目录。
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// 测试配置使用仅当前用户可写的固定权限。
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirEnvironmentNumericBoundaries(t *testing.T) {
	// 使用十进制 MaxInt64 验证环境变量整数转换的合法上边界。
	t.Setenv("ORIGIN_M3_MAX_INT64", strconv.FormatInt(int64(^uint64(0)>>1), 10))
	dir := t.TempDir()
	writeConfig(t, dir, "config.yaml", "value: ${ORIGIN_M3_MAX_INT64}\n")
	// 加载并确认值没有截断或符号变化。
	var got struct{ Value int64 }
	if err := LoadDir(dir, &got); err != nil {
		t.Fatalf("LoadDir 返回错误: %v", err)
	}
	if got.Value != int64(^uint64(0)>>1) {
		t.Fatalf("Value = %d", got.Value)
	}
}
