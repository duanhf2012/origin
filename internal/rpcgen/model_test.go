package rpcgen

import (
	"bytes"
	"fmt"
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

// TestRunWritesOneGeneratedFilePerSource 锁定“声明文件与生成文件一一对应”的公开约定。
func TestRunWritesOneGeneratedFilePerSource(t *testing.T) {
	directory := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/rpcgentest\n\ngo 1.26.5\n",
		"player_service.go": `package contract
import "context"
//origin:rpc
type PlayerService interface { Get(context.Context, int64) string }
`,
		"chat_service.go": `package contract
import "context"
//origin:rpc
type ChatService interface { Send(context.Context, string) }
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, name := range []string{"player_service.rpc.gen.go", "chat_service.rpc.gen.go"} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.HasPrefix(content, []byte(generatedMarker)) {
			t.Fatalf("generated file %s content=%q error=%v", name, content, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "zz_origin_rpc.gen.go")); !os.IsNotExist(err) {
		t.Fatalf("legacy aggregate file exists: %v", err)
	}
}

// TestRunGeneratesOnlyContractPackage 锁定契约与实现分包时的文件所有权：生成器只写
// 契约包，不扫描或改写实现该接口的业务 Service 包。
func TestRunGeneratesOnlyContractPackage(t *testing.T) {
	directory := t.TempDir()
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goSum, err := os.ReadFile(filepath.Join(repositoryRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	rootGoMod, err := os.ReadFile(filepath.Join(repositoryRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	testGoMod := strings.Replace(
		string(rootGoMod),
		"module github.com/duanhf2012/origin/v3",
		"module example.com/game",
		1,
	)
	testGoMod += fmt.Sprintf(
		"\nrequire github.com/duanhf2012/origin/v3 v3.0.0\n"+
			"replace github.com/duanhf2012/origin/v3 => %s\n",
		filepath.ToSlash(repositoryRoot),
	)
	files := map[string]string{
		"go.mod": testGoMod,
		"go.sum": string(goSum),
		filepath.Join("playerapi", "player_service.go"): `package playerapi
import "context"
//origin:rpc
type PlayerService interface { GetPlayer(context.Context, int64) (string, error) }
`,
		filepath.Join("player", "player_service.go"): `package player
import (
	"context"
	"github.com/duanhf2012/origin/v3/service"
	"example.com/game/playerapi"
)
type PlayerService struct { service.Service }
var _ playerapi.PlayerService = (*PlayerService)(nil)
func (*PlayerService) GetPlayer(context.Context, int64) (string, error) { return "player", nil }
`,
	}
	for name, content := range files {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Run(Options{Patterns: []string{"./..."}, Dir: directory}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	contractGenerated := filepath.Join(
		directory,
		"playerapi",
		"player_service.rpc.gen.go",
	)
	content, err := os.ReadFile(contractGenerated)
	if err != nil {
		t.Fatalf("read contract generated file: %v", err)
	}
	if !bytes.Contains(content, []byte("RegisterGeneratedContract")) {
		t.Fatalf("contract descriptor missing:\n%s", content)
	}
	for _, name := range []string{
		filepath.Join("player", "player_service.rpc.gen.go"),
		filepath.Join("player", "player_service.gen.go"),
	} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("business Service unexpectedly generated %s: %v", name, err)
		}
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
	currentLegacy := filepath.Join(currentDirectory, "zz_origin_rpc.gen.go")
	staleGenerated := filepath.Join(staleDirectory, "zz_origin_rpc.gen.go")
	oldContent := []byte(generatedMarker + "\n\npackage contract\n\nconst Old = 1\n")
	newContent := []byte(generatedMarker + "\n\npackage contract\n\nconst New = 2\n")
	for _, path := range []string{currentLegacy, staleGenerated} {
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
	for _, path := range []string{currentLegacy, staleGenerated} {
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
	if _, err := os.Stat(currentLegacy); !os.IsNotExist(err) {
		t.Fatalf("legacy generated still exists: %v", err)
	}
}

func TestGeneratedFilePathFollowsSourceFile(t *testing.T) {
	path, err := generatedFilePath(filepath.Join("playerapi", "player_service.go"))
	if err != nil {
		t.Fatalf("generatedFilePath() error = %v", err)
	}
	want := filepath.Join("playerapi", "player_service.rpc.gen.go")
	if path != want {
		t.Fatalf("generatedFilePath() = %q, want %q", path, want)
	}
	if _, err := generatedFilePath("playerapi/player_service.gen.go"); err == nil {
		t.Fatal("generated source unexpectedly accepted")
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

func TestDefaultServiceNameUsesDeterministicContractRule(t *testing.T) {
	tests := []struct {
		contract string
		want     string
	}{
		{contract: "PlayerRPC", want: "PlayerService"},
		{contract: "PlayerService", want: "PlayerService"},
		{contract: "PlayerServiceRPC", want: "PlayerService"},
		{contract: "DBRPC", want: "DBService"},
		{contract: "Scene", want: "SceneService"},
		{contract: "RPC", want: "RPCService"},
	}
	for _, test := range tests {
		if got := defaultServiceName(test.contract); got != test.want {
			t.Fatalf("defaultServiceName(%q) = %q", test.contract, got)
		}
	}
}

func TestRenderContractIncludesM19BindingRoutingAndPrepare(t *testing.T) {
	contractTypes := types.NewPackage("example.com/game/playerapi", "playerapi")
	contractPackage := &packages.Package{
		PkgPath: "example.com/game/playerapi",
		Name:    "playerapi",
		Types:   contractTypes,
	}
	item := &contract{
		pkg:         contractPackage,
		name:        "PlayerRPC",
		fullName:    "example.com/game/playerapi.PlayerRPC",
		id:          7,
		fingerprint: [32]byte{9},
		methods: []*method{{
			name: "Echo",
			id:   8,
			outputs: []parameter{{
				name: "result1",
				typ:  types.Typ[types.String],
			}},
		}},
	}
	content, err := renderPackage(packageOutput{
		pkg:       contractPackage,
		contracts: []*contract{item},
	})
	if err != nil {
		t.Fatalf("renderPackage() error = %v", err)
	}
	source := string(content)
	required := []string{
		"rpc.GeneratedABIVersion - 3",
		"3 - rpc.GeneratedABIVersion",
		"func BindPlayerRPC(owner service.IService) PlayerRPCClient",
		`rpc.ToService("PlayerService")`,
		"func BindPlayerRPCTo(owner service.IService, serviceName string) PlayerRPCClient",
		"func (client PlayerRPCClient) OnNode(nodeID string) PlayerRPCClient",
		"func (client PlayerRPCClient) IncludeRetired() PlayerRPCClient",
		"func (client PlayerRPCClient) RouteRoundRobin() PlayerRPCClient",
		"func (client PlayerRPCClient) RouteRandom() PlayerRPCClient",
		"func (client PlayerRPCClient) Route(key any) PlayerRPCClient",
		"func (client PlayerRPCClient) RouteBy(selector rpc.RouteSelector) PlayerRPCClient",
		"client.client.PrepareAwait(ctx, playerRPCEchoMethodID)",
		"client.client.PrepareCall(ctx, playerRPCEchoMethodID)",
		"client.client.PrepareAsync(ctx, playerRPCEchoMethodID)",
		"client.client.PrepareNotify(ctx, playerRPCEchoMethodID)",
		"client.client.PrepareBroadcast(ctx, playerRPCEchoMethodID)",
		"rpc.RegisterGeneratedContract(rpc.GeneratedContractDescriptor{",
		`ServiceName:  "PlayerService"`,
		`ContractName: "example.com/game/playerapi.PlayerRPC"`,
		"target, ok := implementation.(PlayerRPC)",
		"return NewPlayerRPCDispatcher(target), true",
		"defer preparedClient.FinishInvocation()",
		"handedOff = true",
	}
	for _, expected := range required {
		if !strings.Contains(source, expected) {
			t.Fatalf("generated source missing %q:\n%s", expected, source)
		}
	}
	if strings.Count(
		source,
		"client.client.PrepareNotify(ctx, playerRPCEchoMethodID)",
	) != 1 {
		t.Fatalf("Broadcast 被错误加入 PrepareNotify:\n%s", source)
	}
	if strings.Count(
		source,
		"client.client.PrepareBroadcast(ctx, playerRPCEchoMethodID)",
	) != 1 {
		t.Fatalf("Broadcast 没有恰好执行一次 PrepareBroadcast:\n%s", source)
	}
	broadcastBody := "preparedClient, err := client.client.PrepareBroadcast(ctx, playerRPCEchoMethodID)\n" +
		"\tif err != nil {\n\t\treturn err\n\t}\n" +
		"\trequest, err := encodePlayerRPCEchoRequest(preparedClient, rpc.CallNotify)"
	if !strings.Contains(source, broadcastBody) || !strings.Contains(
		source,
		"return preparedClient.Broadcast(ctx, playerRPCEchoMethodID, request)",
	) {
		t.Fatalf("Broadcast 没有使用 Prepare 返回的客户端完成编码和提交:\n%s", source)
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
