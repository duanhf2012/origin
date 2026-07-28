package rpcgen

import (
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestStableIDGoldenAndCollisionValidation(t *testing.T) {
	// Golden 数值锁定生成协议，避免重构时无意改变全部线上 ContractID。
	if actual := stableID("contract\x00example.com/game.PlayerRPC"); actual != 0x3ec5a5f3ad102764 {
		t.Fatalf("stableID() = 0x%016x", actual)
	}

	first := &contract{fullName: "a.First", id: 7}
	second := &contract{fullName: "b.Second", id: 7}
	if err := validateIDCollisions([]*contract{first, second}); err == nil ||
		!strings.Contains(err.Error(), "ContractID 碰撞") {
		t.Fatalf("validateIDCollisions() error = %v", err)
	}

	first.id = 1
	first.methods = []*method{{name: "A", id: 9}}
	second.id = 2
	second.methods = []*method{{name: "B", id: 9}}
	if err := validateIDCollisions([]*contract{first, second}); err == nil ||
		!strings.Contains(err.Error(), "MethodID 碰撞") {
		t.Fatalf("validateIDCollisions() method error = %v", err)
	}
}

func TestValidateTypeRejectsEveryUnsupportedFamily(t *testing.T) {
	tests := []struct {
		name string
		typ  types.Type
	}{
		{name: "uintptr", typ: types.Typ[types.Uintptr]},
		{name: "complex64", typ: types.Typ[types.Complex64]},
		{name: "complex128", typ: types.Typ[types.Complex128]},
		{name: "unsafe pointer", typ: types.Typ[types.UnsafePointer]},
		{name: "interface", typ: types.NewInterfaceType(nil, nil)},
		{
			name: "function",
			typ: types.NewSignatureType(
				nil,
				nil,
				nil,
				types.NewTuple(),
				types.NewTuple(),
				false,
			),
		},
		{name: "channel", typ: types.NewChan(types.SendRecv, types.Typ[types.Int])},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateType(test.typ, true, "RPC.input[1]", nil, 0); err == nil {
				t.Fatalf("validateType(%s) unexpectedly succeeded", test.name)
			}
		})
	}
}

func TestValidateTypeSupportsContainersAndRejectsCycles(t *testing.T) {
	// 覆盖普通结构体内 Map、Slice、指针和全部支持基础 Key 的组合。
	pkg := types.NewPackage("example.com/game", "game")
	structure := types.NewStruct(
		[]*types.Var{
			types.NewField(
				token.NoPos,
				pkg,
				"Players",
				types.NewMap(
					types.Typ[types.Int64],
					types.NewSlice(types.NewPointer(types.Typ[types.String])),
				),
				false,
			),
		},
		nil,
	)
	named := types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, "Payload", nil),
		structure,
		nil,
	)
	if err := validateType(named, true, "RPC.input[1]", nil, 0); err != nil {
		t.Fatalf("supported validateType() error = %v", err)
	}

	// 使用具名结构先建立占位，再把导出 Next 字段指回自身，验证对象图循环在生成期失败。
	typeName := types.NewTypeName(token.NoPos, pkg, "Recursive", nil)
	recursive := types.NewNamed(typeName, types.NewStruct(nil, nil), nil)
	recursive.SetUnderlying(types.NewStruct(
		[]*types.Var{
			types.NewField(
				token.NoPos,
				pkg,
				"Next",
				types.NewPointer(recursive),
				false,
			),
		},
		nil,
	))
	if err := validateType(
		recursive,
		true,
		"RPC.input[1]",
		nil,
		0,
	); err == nil || !strings.Contains(err.Error(), "循环") {
		t.Fatalf("recursive validateType() error = %v", err)
	}
}

func TestBuildMethodRejectsInvalidRPCSignatures(t *testing.T) {
	pkg := types.NewPackage("example.com/game", "game")
	owner := &contract{fullName: "example.com/game.PlayerRPC"}

	// 没有 Context 的普通函数不能通过契约入口校验。
	withoutContext := types.NewSignatureType(
		nil,
		nil,
		nil,
		types.NewTuple(
			types.NewParam(token.NoPos, pkg, "id", types.Typ[types.Int64]),
		),
		types.NewTuple(),
		false,
	)
	if _, err := buildMethod(owner, "Load", withoutContext); err == nil ||
		!strings.Contains(err.Error(), "context.Context") {
		t.Fatalf("without-context buildMethod() error = %v", err)
	}

	// error 出现在业务结果前会破坏统一末尾错误语义。
	contextPackage := types.NewPackage("context", "context")
	contextType := types.NewNamed(
		types.NewTypeName(token.NoPos, contextPackage, "Context", nil),
		types.NewInterfaceType(nil, nil),
		nil,
	)
	errorInMiddle := types.NewSignatureType(
		nil,
		nil,
		nil,
		types.NewTuple(
			types.NewParam(token.NoPos, pkg, "ctx", contextType),
		),
		types.NewTuple(
			types.NewParam(
				token.NoPos,
				pkg,
				"",
				types.Universe.Lookup("error").Type(),
			),
			types.NewParam(token.NoPos, pkg, "", types.Typ[types.Int]),
		),
		false,
	)
	if _, err := buildMethod(owner, "Load", errorInMiddle); err == nil ||
		!strings.Contains(err.Error(), "必须位于最后") {
		t.Fatalf("middle-error buildMethod() error = %v", err)
	}
}

// TestRunRejectsAliasAndGenericContracts 验证非法契约返回定位错误，不会因 go/types 类型
// 形态不同而让生成器自身 panic，也不会留下任何生成文件。
func TestRunRejectsAliasAndGenericContracts(t *testing.T) {
	tests := []struct {
		name        string
		declaration string
		want        string
	}{
		{
			name: "alias",
			declaration: `//origin:rpc
type PlayerRPC = interface {
	Get(context.Context, int64) int64
}`,
			want: "不能使用类型别名",
		},
		{
			name: "generic",
			declaration: `//origin:rpc
type PlayerRPC[T any] interface {
	Get(context.Context, T) T
}`,
			want: "不支持泛型 RPC 契约",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// 每个用例建立完全独立且不依赖网络的最小 Module，交给真实 packages.Load。
			directory := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(directory, "go.mod"),
				[]byte("module example.com/rpcgentest\n\ngo 1.26.5\n"),
				0o644,
			); err != nil {
				t.Fatal(err)
			}
			source := "package contract\n\nimport \"context\"\n\n" +
				test.declaration + "\n"
			if err := os.WriteFile(
				filepath.Join(directory, "contract.go"),
				[]byte(source),
				0o644,
			); err != nil {
				t.Fatal(err)
			}

			err := Run(Options{
				Patterns: []string{"./..."},
				Dir:      directory,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
			if _, err := os.Stat(
				filepath.Join(directory, generatedFileName),
			); !os.IsNotExist(err) {
				t.Fatalf("非法契约生成了文件: %v", err)
			}
		})
	}
}

// TestCommitGeneratedCheckReplaceAndDelete 覆盖 --check 不改磁盘、Windows 目标替换以及只删除
// 带完整生成标记的多余文件。它直接验证最终文件提交层，避免依赖完整包加载掩盖分支。
func TestCommitGeneratedCheckReplaceAndDelete(t *testing.T) {
	root := t.TempDir()
	currentDirectory := filepath.Join(root, "current")
	staleDirectory := filepath.Join(root, "stale")
	for _, directory := range []string{currentDirectory, staleDirectory} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	currentSource := filepath.Join(currentDirectory, "contract.go")
	staleSource := filepath.Join(staleDirectory, "contract.go")
	for _, source := range []string{currentSource, staleSource} {
		if err := os.WriteFile(source, []byte("package contract\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	currentGenerated := filepath.Join(currentDirectory, generatedFileName)
	staleGenerated := filepath.Join(staleDirectory, generatedFileName)
	oldContent := []byte(generatedMarker + "\n\npackage contract\n\nconst Old = 1\n")
	newContent := []byte(generatedMarker + "\n\npackage contract\n\nconst New = 2\n")
	for _, path := range []string{currentGenerated, staleGenerated} {
		if err := os.WriteFile(path, oldContent, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scanned := []*packages.Package{
		{GoFiles: []string{currentSource}},
		{GoFiles: []string{staleSource}},
	}
	rendered := map[string][]byte{currentGenerated: newContent}

	// --check 必须报告替换和删除需求，同时保持两个旧文件逐字节不变。
	if err := commitGenerated(scanned, rendered, true); err == nil {
		t.Fatal("commitGenerated(Check) unexpectedly succeeded")
	}
	for _, path := range []string{currentGenerated, staleGenerated} {
		content, err := os.ReadFile(path)
		if err != nil || string(content) != string(oldContent) {
			t.Fatalf("check 修改了 %s: content=%q error=%v", path, content, err)
		}
	}

	// 正式提交必须替换当前结果，并只删除已经确认属于生成器的多余文件。
	if err := commitGenerated(scanned, rendered, false); err != nil {
		t.Fatalf("commitGenerated() error = %v", err)
	}
	content, err := os.ReadFile(currentGenerated)
	if err != nil || string(content) != string(newContent) {
		t.Fatalf("current generated content=%q error=%v", content, err)
	}
	if _, err := os.Stat(staleGenerated); !os.IsNotExist(err) {
		t.Fatalf("stale generated still exists: %v", err)
	}
}

// TestCommitGeneratedRefusesUnmarkedTarget 锁定生成器的文件所有权：即使文件名与默认生成
// 文件相同，只要没有完整标记也不能被 --check 或正式生成覆盖。
func TestCommitGeneratedRefusesUnmarkedTarget(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "contract.go")
	target := filepath.Join(directory, generatedFileName)
	if err := os.WriteFile(source, []byte("package contract\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manual := []byte("package contract\n\nconst Manual = true\n")
	if err := os.WriteFile(target, manual, 0o644); err != nil {
		t.Fatal(err)
	}
	scanned := []*packages.Package{{GoFiles: []string{source}}}
	rendered := map[string][]byte{
		target: []byte(generatedMarker + "\n\npackage contract\n"),
	}
	if err := commitGenerated(scanned, rendered, false); err == nil ||
		!strings.Contains(err.Error(), "不是 origingen 生成文件") {
		t.Fatalf("commitGenerated() error = %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != string(manual) {
		t.Fatalf("manual target content=%q error=%v", content, err)
	}
}

// TestRenderServiceOnlyPackageHasNoUnusedContextImport 覆盖契约与 Service 分包的常见布局。
// 纯实现包只有 RPCDispatcher 适配器，不得生成未使用的 context 导入。
func TestRenderServiceOnlyPackageHasNoUnusedContextImport(t *testing.T) {
	contractTypes := types.NewPackage("example.com/game/contract", "contract")
	serviceTypes := types.NewPackage("example.com/game/player", "player")
	contractPackage := &packages.Package{
		PkgPath: "example.com/game/contract",
		Name:    "contract",
		Types:   contractTypes,
	}
	servicePackage := &packages.Package{
		PkgPath: "example.com/game/player",
		Name:    "player",
		Types:   serviceTypes,
	}
	item := &contract{
		pkg:      contractPackage,
		name:     "PlayerRPC",
		fullName: "example.com/game/contract.PlayerRPC",
	}
	content, err := renderPackage(packageOutput{
		pkg: servicePackage,
		services: []serviceBinding{
			{
				pkg:      servicePackage,
				typeName: "PlayerService",
				contract: item,
			},
		},
	})
	if err != nil {
		t.Fatalf("renderPackage() error = %v", err)
	}
	if strings.Contains(string(content), "context \"context\"") {
		t.Fatalf("纯 Service 包包含未使用 context 导入:\n%s", content)
	}
	if !strings.Contains(
		string(content),
		"contract.NewPlayerRPCDispatcher(service)",
	) {
		t.Fatalf("跨包 Dispatcher 适配器缺失:\n%s", content)
	}
}

func TestCheckedIntegrationGenerationIsCurrent(t *testing.T) {
	err := Run(Options{
		Patterns: []string{"./tests/integration/rpcfixture"},
		Check:    true,
		Dir:      "../..",
	})
	if err != nil {
		t.Fatalf("Run(Check) error = %v", err)
	}
}
