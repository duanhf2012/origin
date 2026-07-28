package rpcgen

import (
	"fmt"
	"go/ast"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	customCodecMarker = "origin:rpc-codec"
	customCodecFormat = "custom-v1"
	maxCodecIDBytes   = 128
)

// customCodec 是一个已经完成全部生成期校验的无状态 Codec Provider。
type customCodec struct {
	provider   *types.Named
	fullName   string
	targetName string
	id         string
	version    uint32
}

// codecRegistry 保存一次 origingen 执行中不可变的自定义 Codec 选择结果。
//
// 目录只在生成冷路径构造和查询，不会进入生成后的 RPC 热路径。
type codecRegistry struct {
	byTarget map[string]*customCodec
	byID     map[string]*customCodec
}

// newCodecRegistry 创建可以安全查询但尚未登记 Provider 的目录。
func newCodecRegistry() *codecRegistry {
	return &codecRegistry{
		byTarget: make(map[string]*customCodec),
		byID:     make(map[string]*customCodec),
	}
}

// lookup 返回 typ 对应的精确具名 Codec；外层指针和容器不会在这里隐式展开。
func (registry *codecRegistry) lookup(typ types.Type) *customCodec {
	if registry == nil || typ == nil {
		return nil
	}
	unalias := types.Unalias(typ)
	named, ok := unalias.(*types.Named)
	if !ok {
		return nil
	}
	return registry.byTarget[canonicalTypeName(named)]
}

// collectCustomCodecs 在契约类型校验之前扫描整个当前 Module，并一次冻结全部 Provider。
func collectCustomCodecs(
	packagesToScan []*packages.Package,
) (*codecRegistry, error) {
	registry := newCodecRegistry()
	for _, pkg := range packagesToScan {
		if pkg.Types == nil || len(pkg.Syntax) == 0 {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				typeDecl, ok := declaration.(*ast.GenDecl)
				if !ok || typeDecl.Tok.String() != "type" {
					continue
				}
				for _, specification := range typeDecl.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok {
						continue
					}
					marker, found, err := parseCustomCodecMarker(
						typeDecl.Doc,
						typeSpec.Doc,
					)
					if err != nil {
						return nil, fmt.Errorf(
							"%s.%s: %w",
							pkg.PkgPath,
							typeSpec.Name.Name,
							err,
						)
					}
					if !found {
						continue
					}
					codec, err := buildCustomCodec(pkg, typeSpec, marker)
					if err != nil {
						return nil, err
					}
					if previous := registry.byID[codec.id]; previous != nil {
						return nil, fmt.Errorf(
							"Codec ID %q 重复: %s 与 %s",
							codec.id,
							previous.fullName,
							codec.fullName,
						)
					}
					if previous := registry.byTarget[codec.targetName]; previous != nil {
						return nil, fmt.Errorf(
							"目标类型 %s 存在多个 Codec: %s 与 %s",
							codec.targetName,
							previous.fullName,
							codec.fullName,
						)
					}
					registry.byID[codec.id] = codec
					registry.byTarget[codec.targetName] = codec
				}
			}
		}
	}
	return registry, nil
}

// customCodecOptions 保存从单条标记中解析出的稳定协议身份。
type customCodecOptions struct {
	id      string
	version uint32
}

// parseCustomCodecMarker 查找并严格解析唯一一条 Provider 标记。
func parseCustomCodecMarker(
	groups ...*ast.CommentGroup,
) (customCodecOptions, bool, error) {
	var marker string
	count := 0
	for _, group := range groups {
		if group == nil {
			continue
		}
		for _, comment := range group.List {
			line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if line == customCodecMarker ||
				strings.HasPrefix(line, customCodecMarker+" ") {
				count++
				marker = line
			}
		}
	}
	if count == 0 {
		return customCodecOptions{}, false, nil
	}
	if count != 1 {
		return customCodecOptions{}, false, fmt.Errorf(
			"只能声明一条 //%s 标记",
			customCodecMarker,
		)
	}

	fields := strings.Fields(marker)
	if len(fields) != 3 || fields[0] != customCodecMarker {
		return customCodecOptions{}, false, fmt.Errorf(
			"Codec 标记必须使用 //%s id=<id> version=<version>",
			customCodecMarker,
		)
	}
	values := make(map[string]string, 2)
	for _, field := range fields[1:] {
		name, value, ok := strings.Cut(field, "=")
		if !ok || value == "" || (name != "id" && name != "version") {
			return customCodecOptions{}, false, fmt.Errorf(
				"Codec 标记包含无效选项 %q",
				field,
			)
		}
		if _, exists := values[name]; exists {
			return customCodecOptions{}, false, fmt.Errorf(
				"Codec 标记重复声明 %s",
				name,
			)
		}
		values[name] = value
	}
	id, idFound := values["id"]
	versionText, versionFound := values["version"]
	if !idFound || !versionFound {
		return customCodecOptions{}, false, fmt.Errorf(
			"Codec 标记必须同时声明 id 和 version",
		)
	}
	if !validCodecID(id) {
		return customCodecOptions{}, false, fmt.Errorf("Codec ID %q 无效", id)
	}
	version, err := strconv.ParseUint(versionText, 10, 32)
	if err != nil || version == 0 {
		return customCodecOptions{}, false, fmt.Errorf(
			"Codec version %q 必须是正 uint32",
			versionText,
		)
	}
	return customCodecOptions{
		id:      id,
		version: uint32(version),
	}, true, nil
}

// validCodecID 执行不需要正则表达式的严格 ASCII Codec ID 校验。
func validCodecID(value string) bool {
	if len(value) == 0 || len(value) > maxCodecIDBytes ||
		!asciiLetter(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if asciiLetter(current) || (current >= '0' && current <= '9') {
			continue
		}
		switch current {
		case '.', '/', '_', '-':
			continue
		default:
			return false
		}
	}
	return true
}

// asciiLetter 报告字符是否为 Codec ID 允许的 ASCII 字母。
func asciiLetter(value byte) bool {
	return (value >= 'a' && value <= 'z') ||
		(value >= 'A' && value <= 'Z')
}

// buildCustomCodec 校验一个标记 Provider，并从三个方法推导唯一目标类型。
func buildCustomCodec(
	pkg *packages.Package,
	typeSpec *ast.TypeSpec,
	options customCodecOptions,
) (*customCodec, error) {
	path := pkg.PkgPath + "." + typeSpec.Name.Name
	if !typeSpec.Name.IsExported() {
		return nil, fmt.Errorf("%s: Codec Provider 必须导出", path)
	}
	if typeSpec.Assign.IsValid() {
		return nil, fmt.Errorf("%s: Codec Provider 不能使用类型别名", path)
	}
	object, ok := pkg.Types.Scope().
		Lookup(typeSpec.Name.Name).(*types.TypeName)
	if !ok || object == nil {
		return nil, fmt.Errorf("%s: 找不到 Codec Provider 类型", path)
	}
	provider, ok := object.Type().(*types.Named)
	if !ok || provider == nil {
		return nil, fmt.Errorf("%s: Codec Provider 必须是具名类型", path)
	}
	if provider.TypeParams() != nil && provider.TypeParams().Len() != 0 {
		return nil, fmt.Errorf("%s: Codec Provider 不能使用泛型", path)
	}
	structure, ok := provider.Underlying().(*types.Struct)
	if !ok || structure.NumFields() != 0 {
		return nil, fmt.Errorf("%s: Codec Provider 必须是无字段空结构体", path)
	}

	sizeTarget, err := codecSizeTarget(provider)
	if err != nil {
		return nil, fmt.Errorf("%s.Size: %w", path, err)
	}
	marshalTarget, err := codecMarshalTarget(provider)
	if err != nil {
		return nil, fmt.Errorf("%s.MarshalTo: %w", path, err)
	}
	unmarshalTarget, err := codecUnmarshalTarget(provider)
	if err != nil {
		return nil, fmt.Errorf("%s.Unmarshal: %w", path, err)
	}
	if !types.Identical(sizeTarget, marshalTarget) ||
		!types.Identical(sizeTarget, unmarshalTarget) {
		return nil, fmt.Errorf("%s: 三个 Codec 方法的目标类型不一致", path)
	}
	target, ok := types.Unalias(sizeTarget).(*types.Named)
	if !ok || target == nil {
		return nil, fmt.Errorf("%s: Codec 目标必须是具名非指针类型", path)
	}
	if err := validateCustomCodecTarget(target); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return &customCodec{
		provider:   provider,
		fullName:   path,
		targetName: canonicalTypeName(target),
		id:         options.id,
		version:    options.version,
	}, nil
}

// codecSizeTarget 校验 Size(*T) (int, error) 并返回 T。
func codecSizeTarget(provider *types.Named) (types.Type, error) {
	signature, err := codecMethodSignature(provider, "Size")
	if err != nil {
		return nil, err
	}
	if signature.Params().Len() != 1 ||
		signature.Results().Len() != 2 ||
		!types.Identical(signature.Results().At(0).Type(), types.Typ[types.Int]) ||
		!isError(signature.Results().At(1).Type()) {
		return nil, fmt.Errorf("签名必须是 Size(*T) (int, error)")
	}
	return codecPointerElement(signature.Params().At(0).Type())
}

// codecMarshalTarget 校验 MarshalTo([]byte, *T) (int, error) 并返回 T。
func codecMarshalTarget(provider *types.Named) (types.Type, error) {
	signature, err := codecMethodSignature(provider, "MarshalTo")
	if err != nil {
		return nil, err
	}
	if signature.Params().Len() != 2 ||
		!isBuiltinByteSlice(signature.Params().At(0).Type()) ||
		signature.Results().Len() != 2 ||
		!types.Identical(signature.Results().At(0).Type(), types.Typ[types.Int]) ||
		!isError(signature.Results().At(1).Type()) {
		return nil, fmt.Errorf(
			"签名必须是 MarshalTo([]byte, *T) (int, error)",
		)
	}
	return codecPointerElement(signature.Params().At(1).Type())
}

// codecUnmarshalTarget 校验 Unmarshal([]byte, *T) error 并返回 T。
func codecUnmarshalTarget(provider *types.Named) (types.Type, error) {
	signature, err := codecMethodSignature(provider, "Unmarshal")
	if err != nil {
		return nil, err
	}
	if signature.Params().Len() != 2 ||
		!isBuiltinByteSlice(signature.Params().At(0).Type()) ||
		signature.Results().Len() != 1 ||
		!isError(signature.Results().At(0).Type()) {
		return nil, fmt.Errorf("签名必须是 Unmarshal([]byte, *T) error")
	}
	return codecPointerElement(signature.Params().At(1).Type())
}

// codecMethodSignature 只从值方法集取得方法，因而会拒绝指针接收者 Provider。
func codecMethodSignature(
	provider *types.Named,
	name string,
) (*types.Signature, error) {
	selection := types.NewMethodSet(provider).Lookup(nil, name)
	if selection == nil {
		return nil, fmt.Errorf("缺少值接收者方法")
	}
	function, ok := selection.Obj().(*types.Func)
	if !ok {
		return nil, fmt.Errorf("必须是方法")
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Variadic() ||
		(signature.TypeParams() != nil && signature.TypeParams().Len() != 0) {
		return nil, fmt.Errorf("方法不能是泛型或可变参数")
	}
	return signature, nil
}

// codecPointerElement 要求方法参数严格为 *T，并返回去除别名后的 T。
func codecPointerElement(typ types.Type) (types.Type, error) {
	pointer, ok := types.Unalias(typ).(*types.Pointer)
	if !ok {
		return nil, fmt.Errorf("目标参数必须是指针")
	}
	return types.Unalias(pointer.Elem()), nil
}

// isBuiltinByteSlice 只接受预声明 byte 的普通 []byte，不接受具名 Slice。
func isBuiltinByteSlice(typ types.Type) bool {
	slice, ok := types.Unalias(typ).(*types.Slice)
	if !ok {
		return false
	}
	return types.Identical(slice.Elem(), types.Typ[types.Byte])
}

// validateCustomCodecTarget 拒绝没有稳定跨进程值语义的具名目标。
func validateCustomCodecTarget(target *types.Named) error {
	if target.TypeParams() != nil && target.TypeParams().Len() != 0 {
		return fmt.Errorf("Codec 目标不能包含类型参数")
	}
	switch underlying := target.Underlying().(type) {
	case *types.Basic:
		if underlying.Kind() == types.UnsafePointer {
			return fmt.Errorf("Codec 目标不能是 unsafe.Pointer")
		}
		return nil
	case *types.Array, *types.Slice, *types.Map, *types.Struct:
		return nil
	case *types.Interface:
		return fmt.Errorf("Codec 目标不能是接口")
	case *types.Signature:
		return fmt.Errorf("Codec 目标不能是函数")
	case *types.Chan:
		return fmt.Errorf("Codec 目标不能是 Channel")
	case *types.Pointer:
		return fmt.Errorf("Codec 目标不能是具名指针")
	default:
		return fmt.Errorf(
			"Codec 目标类型 %s 不受支持",
			canonicalTypeName(target),
		)
	}
}
