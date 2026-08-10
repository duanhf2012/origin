package rpcgen

import (
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestValidateCustomCodecTargetFamilies 锁定显式 Codec 可以接管具名值类型，但不能把
// 接口、函数、Channel、指针或 unsafe.Pointer 伪装成稳定 RPC 值。
func TestValidateCustomCodecTargetFamilies(t *testing.T) {
	t.Parallel()

	pkg := types.NewPackage("example.com/model", "model")
	named := func(name string, underlying types.Type) *types.Named {
		return types.NewNamed(
			types.NewTypeName(token.NoPos, pkg, name, nil),
			underlying,
			nil,
		)
	}
	valid := []*types.Named{
		named("Scalar", types.Typ[types.Complex64]),
		named("Array", types.NewArray(types.Typ[types.Byte], 16)),
		named("Slice", types.NewSlice(types.Typ[types.Byte])),
		named(
			"Map",
			types.NewMap(types.Typ[types.String], types.Typ[types.Int]),
		),
		named("Struct", types.NewStruct(nil, nil)),
	}
	for _, target := range valid {
		if err := validateCustomCodecTarget(target); err != nil {
			t.Errorf("valid target %s error = %v", target.Obj().Name(), err)
		}
	}

	invalid := []*types.Named{
		named("Unsafe", types.Typ[types.UnsafePointer]),
		named("Interface", types.NewInterfaceType(nil, nil)),
		named(
			"Function",
			types.NewSignatureType(
				nil,
				nil,
				nil,
				types.NewTuple(),
				types.NewTuple(),
				false,
			),
		),
		named("Channel", types.NewChan(types.SendRecv, types.Typ[types.Int])),
		named("Pointer", types.NewPointer(types.Typ[types.Int])),
	}
	for _, target := range invalid {
		if err := validateCustomCodecTarget(target); err == nil {
			t.Errorf("invalid target %s unexpectedly succeeded", target.Obj().Name())
		}
	}
}

// TestEmptyCodecRegistryKeepsBaseSchema 验证没有自定义 Codec 时，扩展接缝不会改变已冻结的
// 基础类型 Schema 文本和后续契约指纹输入。
func TestEmptyCodecRegistryKeepsBaseSchema(t *testing.T) {
	t.Parallel()

	item := &contract{
		fullName: "example.com/game.PlayerRPC",
		methods: []*method{
			{
				name: "Echo",
				inputs: []parameter{
					{typ: types.Typ[types.Int64]},
				},
				outputs: []parameter{
					{typ: types.Typ[types.String]},
				},
			},
		},
	}
	want := "origin-rpc-schema-v1\n" +
		"example.com/game.PlayerRPC\n" +
		"Echo(int64)->(string)\n"
	if got := contractSchema(item); got != want {
		t.Fatalf("base schema changed:\n%s\nwant:\n%s", got, want)
	}
	item.codecs = newCodecRegistry()
	if got := contractSchema(item); got != want {
		t.Fatalf("empty registry changed base schema:\n%s\nwant:\n%s", got, want)
	}
}

// TestParseCustomCodecMarker 验证标记顺序无关，但缺失、重复、未知和非法 ID/版本都会失败。
func TestParseCustomCodecMarker(t *testing.T) {
	t.Parallel()

	valid := []string{
		"//origin:rpc-codec id=game.time version=1",
		"//origin:rpc-codec version=4294967295 id=Game/time-v2",
	}
	for _, line := range valid {
		options, found, err := parseCustomCodecMarker(commentGroup(line))
		if err != nil || !found || options.id == "" || options.version == 0 {
			t.Errorf("valid marker %q = %+v, %t, %v", line, options, found, err)
		}
	}

	invalid := []string{
		"//origin:rpc-codec",
		"//origin:rpc-codec id=1game version=1",
		"//origin:rpc-codec id=game@time version=1",
		"//origin:rpc-codec id=game.time version=0",
		"//origin:rpc-codec id=game.time version=-1",
		"//origin:rpc-codec id=game.time version=4294967296",
		"//origin:rpc-codec id=game.time unknown=1",
		"//origin:rpc-codec id=game.time id=other",
	}
	for _, line := range invalid {
		if _, found, err := parseCustomCodecMarker(
			commentGroup(line),
		); err == nil {
			t.Errorf("invalid marker %q found=%t error=%v", line, found, err)
		}
	}

	if _, found, err := parseCustomCodecMarker(
		commentGroup(
			"//origin:rpc-codec id=game.a version=1",
			"//origin:rpc-codec id=game.b version=1",
		),
	); found || err == nil {
		t.Fatalf("duplicate markers found=%t error=%v", found, err)
	}
}

// TestRunRejectsInvalidCustomCodecProviders 通过真实 packages.Load 覆盖 Provider 结构、方法、
// 目标和冲突错误，并确认失败时不会生成部分文件。
func TestRunRejectsInvalidCustomCodecProviders(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "provider not exported",
			source: codecTestSource(`
//origin:rpc-codec id=game.special version=1
type specialCodec struct{}
func (specialCodec) Size(*Special) (int, error) { return 0, nil }
func (specialCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (specialCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "Provider 必须导出",
		},
		{
			name: "provider has field",
			source: codecTestSource(`
//origin:rpc-codec id=game.special version=1
type SpecialCodec struct{ State int }
func (SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "无字段空结构体",
		},
		{
			name: "provider alias",
			source: codecTestSource(`
type CodecBase struct{}
//origin:rpc-codec id=game.special version=1
type SpecialCodec = CodecBase
`),
			want: "不能使用类型别名",
		},
		{
			name: "generic provider",
			source: codecTestSource(`
//origin:rpc-codec id=game.special version=1
type SpecialCodec[T any] struct{}
func (SpecialCodec[T]) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec[T]) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (SpecialCodec[T]) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "不能使用泛型",
		},
		{
			name: "pointer receiver",
			source: codecTestSource(`
//origin:rpc-codec id=game.special version=1
type SpecialCodec struct{}
func (*SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (*SpecialCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (*SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "缺少值接收者方法",
		},
		{
			name: "marshal signature",
			source: codecTestSource(`
//origin:rpc-codec id=game.special version=1
type SpecialCodec struct{}
func (SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec) MarshalTo(string, *Special) (int, error) { return 0, nil }
func (SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "MarshalTo([]byte, *T)",
		},
		{
			name: "method target mismatch",
			source: codecTestSource(`
type Other struct{}
//origin:rpc-codec id=game.special version=1
type SpecialCodec struct{}
func (SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec) MarshalTo([]byte, *Other) (int, error) { return 0, nil }
func (SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "目标类型不一致",
		},
		{
			name: "interface target",
			source: `package contract
type Special interface{ Value() int }
//origin:rpc-codec id=game.special version=1
type SpecialCodec struct{}
func (SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
`,
			want: "目标不能是接口",
		},
		{
			name: "duplicate codec id",
			source: codecTestSource(`
type Other struct{ Value int }
//origin:rpc-codec id=game.same version=1
type SpecialCodec struct{}
func (SpecialCodec) Size(*Special) (int, error) { return 0, nil }
func (SpecialCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (SpecialCodec) Unmarshal([]byte, *Special) error { return nil }
//origin:rpc-codec id=game.same version=2
type OtherCodec struct{}
func (OtherCodec) Size(*Other) (int, error) { return 0, nil }
func (OtherCodec) MarshalTo([]byte, *Other) (int, error) { return 0, nil }
func (OtherCodec) Unmarshal([]byte, *Other) error { return nil }
`),
			want: "Codec ID",
		},
		{
			name: "duplicate target",
			source: codecTestSource(`
//origin:rpc-codec id=game.first version=1
type FirstCodec struct{}
func (FirstCodec) Size(*Special) (int, error) { return 0, nil }
func (FirstCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (FirstCodec) Unmarshal([]byte, *Special) error { return nil }
//origin:rpc-codec id=game.second version=1
type SecondCodec struct{}
func (SecondCodec) Size(*Special) (int, error) { return 0, nil }
func (SecondCodec) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (SecondCodec) Unmarshal([]byte, *Special) error { return nil }
`),
			want: "存在多个 Codec",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := writeCodecTestModule(t)
			writeCodecTestFile(t, directory, "contract.go", test.source)
			err := Run(Options{
				Patterns: []string{"./..."},
				Dir:      directory,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(
				filepath.Join(directory, generatedFileName),
			); !os.IsNotExist(statErr) {
				t.Fatalf("非法 Provider 生成了文件: %v", statErr)
			}
		})
	}
}

// TestCustomCodecFingerprintIdentity 验证 Provider Go 名称不是协议身份，而 Codec version
// 变化必然改变完整契约指纹。
func TestCustomCodecFingerprintIdentity(t *testing.T) {
	directory := writeCodecTestModule(t)
	path := filepath.Join(directory, "contract.go")
	writeCodecTestFile(
		t,
		directory,
		"contract.go",
		validCodecContractSource("SpecialCodec", 1),
	)
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	first := generatedFingerprintLine(t, directory)

	// 只重命名 Provider，Codec ID、版本和目标类型保持不变，指纹必须稳定。
	if err := os.WriteFile(
		path,
		[]byte(validCodecContractSource("RenamedCodec", 1)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("renamed Run() error = %v", err)
	}
	renamed := generatedFingerprintLine(t, directory)
	if renamed != first {
		t.Fatalf("Provider rename changed fingerprint:\n%s\n%s", first, renamed)
	}

	// 修改协议版本必须改变指纹，阻止新旧线格式静默互通。
	if err := os.WriteFile(
		path,
		[]byte(validCodecContractSource("RenamedCodec", 2)),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("version Run() error = %v", err)
	}
	versioned := generatedFingerprintLine(t, directory)
	if versioned == first {
		t.Fatalf("Codec version did not change fingerprint: %s", versioned)
	}

	// Codec ID 同样是协议身份；保持目标和版本不变时，仅修改 ID 也必须改变指纹。
	idChangedSource := strings.Replace(
		validCodecContractSource("RenamedCodec", 1),
		"id=game.special",
		"id=game.special-v2",
		1,
	)
	if err := os.WriteFile(path, []byte(idChangedSource), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("Codec ID Run() error = %v", err)
	}
	idChanged := generatedFingerprintLine(t, directory)
	if idChanged == first {
		t.Fatalf("Codec ID did not change fingerprint: %s", idChanged)
	}
}

// TestRunGeneratesCrossPackageCustomCodecCall 验证契约可以使用其他包声明的 Provider，且
// 生成代码导入具体包并静态调用，不生成运行时注册语句。
func TestRunGeneratesCrossPackageCustomCodecCall(t *testing.T) {
	directory := writeCodecTestModule(t)
	codecDirectory := filepath.Join(directory, "codec")
	contractDirectory := filepath.Join(directory, "contract")
	if err := os.MkdirAll(codecDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(contractDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCodecTestFile(t, codecDirectory, "codec.go", `package codec
import "time"
//origin:rpc-codec id=game.time version=1
type TimeCodec struct{}
func (TimeCodec) Size(*time.Time) (int, error) { return 8, nil }
func (TimeCodec) MarshalTo([]byte, *time.Time) (int, error) { return 8, nil }
func (TimeCodec) Unmarshal([]byte, *time.Time) error { return nil }
`)
	writeCodecTestFile(t, contractDirectory, "contract.go", `package contract
import (
	"context"
	"time"
)
//origin:rpc
type TimeRPC interface {
	Echo(context.Context, time.Time) time.Time
}
`)
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(
		filepath.Join(contractDirectory, generatedFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "codec.TimeCodec{}.Size") ||
		!strings.Contains(text, "codec.TimeCodec{}.MarshalTo") ||
		!strings.Contains(text, "codec.TimeCodec{}.Unmarshal") {
		t.Fatalf("cross-package static calls missing:\n%s", text)
	}
	if strings.Contains(text, "RegisterCodec") ||
		strings.Contains(text, "StaticCodec[") {
		t.Fatalf("generated runtime codec dispatch found:\n%s", text)
	}
}

// TestCustomCodecProviderPackageDoesNotChangeFingerprint 验证 Provider 的 Go 包只是生成期
// 实现位置；Codec ID、版本和目标类型不变时，移动 Provider 不会制造协议不兼容。
func TestCustomCodecProviderPackageDoesNotChangeFingerprint(t *testing.T) {
	directory := writeCodecTestModule(t)
	modelDirectory := filepath.Join(directory, "model")
	contractDirectory := filepath.Join(directory, "contract")
	firstCodecDirectory := filepath.Join(directory, "codeca")
	secondCodecDirectory := filepath.Join(directory, "codecb")
	for _, path := range []string{
		modelDirectory,
		contractDirectory,
		firstCodecDirectory,
		secondCodecDirectory,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCodecTestFile(
		t,
		modelDirectory,
		"model.go",
		"package model\ntype Special struct{ Value int }\n",
	)
	writeCodecTestFile(
		t,
		contractDirectory,
		"contract.go",
		`package contract
import (
	"context"
	"example.com/rpcgentest/model"
)
//origin:rpc
type SpecialRPC interface {
	Echo(context.Context, model.Special) model.Special
}
`,
	)
	writeCodecTestFile(
		t,
		firstCodecDirectory,
		"codec.go",
		externalProviderSource("codeca", "SpecialCodec"),
	)
	writeCodecTestFile(t, secondCodecDirectory, "empty.go", "package codecb\n")

	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("first provider Run() error = %v", err)
	}
	first := generatedFingerprintLine(t, contractDirectory)

	// 先移除旧包中的标记和 Provider，再在另一个包声明等价 Provider，避免生成范围内
	// 短暂出现同一目标的两个 Codec。
	writeCodecTestFile(t, firstCodecDirectory, "codec.go", "package codeca\n")
	writeCodecTestFile(
		t,
		secondCodecDirectory,
		"codec.go",
		externalProviderSource("codecb", "MovedCodec"),
	)
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("moved provider Run() error = %v", err)
	}
	moved := generatedFingerprintLine(t, contractDirectory)
	if moved != first {
		t.Fatalf("Provider package move changed fingerprint:\n%s\n%s", first, moved)
	}
}

// commentGroup 为标记解析单元测试构造与 go/ast 等价的行注释组。
func commentGroup(lines ...string) *ast.CommentGroup {
	group := &ast.CommentGroup{List: make([]*ast.Comment, 0, len(lines))}
	for _, line := range lines {
		group.List = append(group.List, &ast.Comment{Text: line})
	}
	return group
}

// writeCodecTestModule 创建不依赖网络的最小 Go Module。
func writeCodecTestModule(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	writeCodecTestFile(
		t,
		directory,
		"go.mod",
		"module example.com/rpcgentest\n\ngo 1.26.5\n",
	)
	return directory
}

// writeCodecTestFile 负责测试夹具文件创建，并让调用处只表达当前场景内容。
func writeCodecTestFile(
	t *testing.T,
	directory string,
	name string,
	content string,
) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(directory, name),
		[]byte(content),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
}

// codecTestSource 把合法目标和 RPC 契约与待测 Provider 组合成单包源码。
func codecTestSource(provider string) string {
	return `package contract
import "context"
type Special struct{ hidden int }
` + provider + `
//origin:rpc
type SpecialRPC interface {
	Echo(context.Context, Special) Special
}
`
}

// validCodecContractSource 生成只改变 Provider 名称或版本的稳定契约输入。
func validCodecContractSource(provider string, version uint32) string {
	return `package contract
import "context"
type Special struct{ hidden int }
//origin:rpc-codec id=game.special version=` +
		strconv.FormatUint(uint64(version), 10) + `
type ` + provider + ` struct{}
func (` + provider + `) Size(*Special) (int, error) { return 0, nil }
func (` + provider + `) MarshalTo([]byte, *Special) (int, error) { return 0, nil }
func (` + provider + `) Unmarshal([]byte, *Special) error { return nil }
//origin:rpc
type SpecialRPC interface {
	Echo(context.Context, Special) Special
}
`
}

// externalProviderSource 生成以稳定 Codec 身份接管 model.Special 的跨包 Provider。
func externalProviderSource(packageName, provider string) string {
	return `package ` + packageName + `
import "example.com/rpcgentest/model"
//origin:rpc-codec id=game.external-special version=1
type ` + provider + ` struct{}
func (` + provider + `) Size(*model.Special) (int, error) { return 0, nil }
func (` + provider + `) MarshalTo([]byte, *model.Special) (int, error) {
	return 0, nil
}
func (` + provider + `) Unmarshal([]byte, *model.Special) error { return nil }
`
}

// generatedFingerprintLine 提取生成结果中唯一契约指纹行。
func generatedFingerprintLine(t *testing.T, directory string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, generatedFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.Contains(line, "Fingerprint =") {
			return line
		}
	}
	t.Fatalf("generated fingerprint not found:\n%s", content)
	return ""
}
