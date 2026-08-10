package rpcgen

import (
	"fmt"
	"go/types"
	"strconv"
	"strings"
)

// isContext 精确识别标准库 context.Context，不接受同名业务接口。
func isContext(typ types.Type) bool {
	named, ok := typ.(*types.Named)
	return ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == "context" &&
		named.Obj().Name() == "Context"
}

// isError 使用类型等价而不是名称比较识别预声明 error。
func isError(typ types.Type) bool {
	return types.Identical(typ, types.Universe.Lookup("error").Type())
}

// validateType 递归拒绝运行时无法静态编码的类型。
//
// topLevel 只在 RPC 参数或业务输出位置为 true；只有该位置允许把 Protobuf 走官方
// proto Codec。进入普通结构体或容器后，Protobuf 生成类型按普通 Go 结构体展开。
func validateType(
	typ types.Type,
	topLevel bool,
	path string,
	stack map[types.Type]bool,
	depth int,
) error {
	return validateTypeWithCodecs(
		typ,
		topLevel,
		path,
		stack,
		depth,
		nil,
	)
}

// validateTypeWithCodecs 在内置规则前选择精确具名自定义 Codec。
func validateTypeWithCodecs(
	typ types.Type,
	topLevel bool,
	path string,
	stack map[types.Type]bool,
	depth int,
	codecs *codecRegistry,
) error {
	if depth > maxTypeDepth {
		return fmt.Errorf("%s: 类型嵌套超过 %d 层", path, maxTypeDepth)
	}
	if stack == nil {
		stack = make(map[types.Type]bool)
	}
	// 自定义 Codec 把目标具名值视为一个完整叶子，不再检查其内部未导出字段或
	// Protobuf Opaque 表示；外层指针和容器会先在递归调用处保持当前语义。
	if codecs.lookup(typ) != nil {
		return nil
	}
	if topLevel && isProtoType(typ) {
		return nil
	}

	// 具名基础类型沿用其基础线值；具名结构、Slice、Map 等继续验证底层图。
	underlying := typ
	if named, ok := typ.(*types.Named); ok {
		underlying = named.Underlying()
	}
	switch value := underlying.(type) {
	case *types.Basic:
		if supportedBasic(value.Kind()) {
			return nil
		}
		return fmt.Errorf("%s: 不支持类型 %s", path, types.TypeString(typ, nil))
	case *types.Pointer:
		return validateNested(
			value.Elem(),
			path+"*",
			stack,
			depth,
			codecs,
		)
	case *types.Array:
		return validateNested(
			value.Elem(),
			path+"[]",
			stack,
			depth,
			codecs,
		)
	case *types.Slice:
		return validateNested(
			value.Elem(),
			path+"[]",
			stack,
			depth,
			codecs,
		)
	case *types.Map:
		keyCodec := codecs.lookup(value.Key())
		if (keyCodec == nil && !supportedMapKey(value.Key())) ||
			(keyCodec != nil && !types.Comparable(value.Key())) {
			return fmt.Errorf(
				"%s.key: Map Key 不支持类型 %s",
				path,
				types.TypeString(value.Key(), nil),
			)
		}
		if err := validateNested(
			value.Key(),
			path+".key",
			stack,
			depth,
			codecs,
		); err != nil {
			return err
		}
		return validateNested(
			value.Elem(),
			path+".value",
			stack,
			depth,
			codecs,
		)
	case *types.Struct:
		if stack[typ] {
			return fmt.Errorf("%s: 不支持循环对象图 %s", path, types.TypeString(typ, nil))
		}
		stack[typ] = true
		defer delete(stack, typ)
		exported := 0
		for index := 0; index < value.NumFields(); index++ {
			field := value.Field(index)
			if !field.Exported() {
				continue
			}
			if field.Embedded() {
				return fmt.Errorf(
					"%s.%s: RPC 契约不支持匿名嵌入字段",
					path,
					field.Name(),
				)
			}
			exported++
			if err := validateTypeWithCodecs(
				field.Type(),
				false,
				path+"."+field.Name(),
				stack,
				depth+1,
				codecs,
			); err != nil {
				return err
			}
		}
		if exported == 0 {
			return fmt.Errorf(
				"%s: 普通结构体必须至少包含一个导出字段；Opaque Protobuf 首版不支持",
				path,
			)
		}
		return nil
	default:
		return fmt.Errorf("%s: 不支持类型 %s", path, types.TypeString(typ, nil))
	}
}

// validateNested 进入普通 Go 嵌套路径，并统一推进类型深度。
func validateNested(
	typ types.Type,
	path string,
	stack map[types.Type]bool,
	depth int,
	codecs *codecRegistry,
) error {
	return validateTypeWithCodecs(
		typ,
		false,
		path,
		stack,
		depth+1,
		codecs,
	)
}

// supportedBasic 列出当前具有固定线值的全部 Go 基础类型。
func supportedBasic(kind types.BasicKind) bool {
	switch kind {
	case types.Bool,
		types.Int,
		types.Int8,
		types.Int16,
		types.Int32,
		types.Int64,
		types.Uint,
		types.Uint8,
		types.Uint16,
		types.Uint32,
		types.Uint64,
		types.Float32,
		types.Float64,
		types.String:
		return true
	default:
		return false
	}
}

// supportedMapKey 把 Map Key 限制到已经支持静态编码的可比较基础类型。
func supportedMapKey(typ types.Type) bool {
	underlying := typ.Underlying()
	basic, ok := underlying.(*types.Basic)
	return ok && supportedBasic(basic.Kind())
}

// isProtoType 只按官方 ProtoReflect 方法识别顶层 Protobuf，不依赖生成 API 结构字段。
func isProtoType(typ types.Type) bool {
	if protoReflectMethod(typ) {
		return true
	}
	if _, ok := typ.(*types.Named); ok {
		return protoReflectMethod(types.NewPointer(typ))
	}
	return false
}

// protoReflectMethod 要求方法精确返回官方 protoreflect.Message，避免仅同名业务方法被
// 错判为 Protobuf。
func protoReflectMethod(typ types.Type) bool {
	object, _, found := types.LookupFieldOrMethod(
		typ,
		true,
		nil,
		"ProtoReflect",
	)
	if !found {
		return false
	}
	function, ok := object.(*types.Func)
	if !ok {
		return false
	}
	signature, ok := function.Type().(*types.Signature)
	if !ok || signature.Params().Len() != 0 || signature.Results().Len() != 1 {
		return false
	}
	result, ok := signature.Results().At(0).Type().(*types.Named)
	return ok && result.Obj().Pkg() != nil &&
		result.Obj().Pkg().Path() == "google.golang.org/protobuf/reflect/protoreflect" &&
		result.Obj().Name() == "Message"
}

// schemaType 为完整契约指纹建立包含包路径、字段顺序和容器结构的稳定描述。
func schemaType(typ types.Type, topLevel bool, stack map[types.Type]bool) string {
	return schemaTypeWithCodecs(typ, topLevel, stack, nil)
}

// schemaTypeWithCodecs 把自定义 Codec 协议身份写入完整契约 Schema。
func schemaTypeWithCodecs(
	typ types.Type,
	topLevel bool,
	stack map[types.Type]bool,
	codecs *codecRegistry,
) string {
	if codec := codecs.lookup(typ); codec != nil {
		return customCodecFormat + ":" + codec.id + "@" +
			strconv.FormatUint(uint64(codec.version), 10) + ":" +
			codec.targetName
	}
	if topLevel && isProtoType(typ) {
		return "proto:" + canonicalTypeName(typ)
	}
	if stack == nil {
		stack = make(map[types.Type]bool)
	}
	var prefix string
	if named, ok := typ.(*types.Named); ok {
		prefix = canonicalTypeName(named) + "="
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		return prefix + value.Name()
	case *types.Pointer:
		return prefix + "*" + schemaTypeWithCodecs(
			value.Elem(),
			false,
			stack,
			codecs,
		)
	case *types.Array:
		return fmt.Sprintf(
			"%s[%d]%s",
			prefix,
			value.Len(),
			schemaTypeWithCodecs(value.Elem(), false, stack, codecs),
		)
	case *types.Slice:
		return prefix + "[]" + schemaTypeWithCodecs(
			value.Elem(),
			false,
			stack,
			codecs,
		)
	case *types.Map:
		return prefix + "map[" +
			schemaTypeWithCodecs(value.Key(), false, stack, codecs) + "]" +
			schemaTypeWithCodecs(value.Elem(), false, stack, codecs)
	case *types.Struct:
		if stack[typ] {
			return prefix + "<cycle>"
		}
		stack[typ] = true
		defer delete(stack, typ)
		var fields []string
		for index := 0; index < value.NumFields(); index++ {
			field := value.Field(index)
			if !field.Exported() {
				continue
			}
			fields = append(
				fields,
				field.Name()+":"+schemaTypeWithCodecs(
					field.Type(),
					false,
					stack,
					codecs,
				),
			)
		}
		return prefix + "struct{" + strings.Join(fields, ";") + "}"
	default:
		return prefix + types.TypeString(typ, packagePathQualifier)
	}
}

// canonicalTypeName 使用完整导入路径表示具名类型，并保留指针层级。
func canonicalTypeName(typ types.Type) string {
	switch value := typ.(type) {
	case *types.Pointer:
		return "*" + canonicalTypeName(value.Elem())
	case *types.Named:
		if value.Obj().Pkg() == nil {
			return value.Obj().Name()
		}
		return value.Obj().Pkg().Path() + "." + value.Obj().Name()
	default:
		return types.TypeString(typ, packagePathQualifier)
	}
}

// packagePathQualifier 为指纹 Schema 提供不受本地导入别名影响的完整包路径。
func packagePathQualifier(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

// minEncodedSize 返回一个值在普通 Go Codec 中必然占用的最小字节数。
func minEncodedSize(typ types.Type, topLevel bool) int {
	return minEncodedSizeWithCodecs(typ, topLevel, nil)
}

// minEncodedSizeWithCodecs 返回包含自定义长度前缀后的最小线格式尺寸。
func minEncodedSizeWithCodecs(
	typ types.Type,
	topLevel bool,
	codecs *codecRegistry,
) int {
	if codecs.lookup(typ) != nil {
		return 4
	}
	if topLevel && isProtoType(typ) {
		return 4
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		switch value.Kind() {
		case types.Bool, types.Int8, types.Uint8:
			return 1
		case types.Int16, types.Uint16:
			return 2
		case types.Int32, types.Uint32, types.Float32:
			return 4
		case types.Int, types.Uint, types.Int64, types.Uint64, types.Float64:
			return 8
		case types.String:
			return 4
		}
	case *types.Pointer:
		return 1
	case *types.Array:
		element := minEncodedSizeWithCodecs(value.Elem(), false, codecs)
		if element == 0 || value.Len() > int64(^uint(0)>>1)/int64(element) {
			return 0
		}
		return int(value.Len()) * element
	case *types.Slice, *types.Map:
		return 4
	case *types.Struct:
		total := 0
		for index := 0; index < value.NumFields(); index++ {
			field := value.Field(index)
			if field.Exported() {
				total += minEncodedSizeWithCodecs(
					field.Type(),
					false,
					codecs,
				)
			}
		}
		return total
	}
	return 0
}

// exportedFields 返回普通结构体按声明顺序排列的逻辑字段。
func exportedFields(typ types.Type) []*types.Var {
	value, _ := typ.Underlying().(*types.Struct)
	fields := make([]*types.Var, 0, value.NumFields())
	for index := 0; index < value.NumFields(); index++ {
		field := value.Field(index)
		if field.Exported() {
			fields = append(fields, field)
		}
	}
	return fields
}

// basicMethodSuffix 映射到 rpc.Sizer/Writer/Reader 的固定宽度方法名。
func basicMethodSuffix(typ types.Type) string {
	basic, _ := typ.Underlying().(*types.Basic)
	switch basic.Kind() {
	case types.Bool:
		return "Bool"
	case types.Int:
		return "Int"
	case types.Int8:
		return "Int8"
	case types.Int16:
		return "Int16"
	case types.Int32:
		return "Int32"
	case types.Int64:
		return "Int64"
	case types.Uint:
		return "Uint"
	case types.Uint8:
		return "Uint8"
	case types.Uint16:
		return "Uint16"
	case types.Uint32:
		return "Uint32"
	case types.Uint64:
		return "Uint64"
	case types.Float32:
		return "Float32"
	case types.Float64:
		return "Float64"
	case types.String:
		return "String"
	default:
		panic("rpcgen: unsupported basic type")
	}
}

// isByteSlice 识别 []byte 及其具名定义，选择保留 nil/空语义的专用路径。
func isByteSlice(typ types.Type) bool {
	slice, ok := typ.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	basic, ok := slice.Elem().Underlying().(*types.Basic)
	return ok && basic.Kind() == types.Uint8
}
