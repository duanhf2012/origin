package rpcgen

import (
	"fmt"
	"go/types"
)

// next 为当前生成函数分配不会与前序局部变量冲突的确定性名称。
func (render *renderer) next(prefix string) string {
	render.counter++
	return fmt.Sprintf("%s%d", prefix, render.counter)
}

// emitCodecError 根据请求或响应编码器的不同返回签名生成大小计算错误分支。
func (render *renderer) emitCodecError(indent, call string) {
	if render.response {
		fmt.Fprintf(
			render.body,
			"%sif err := %s; err != nil { return err }\n",
			indent,
			call,
		)
		return
	}
	fmt.Fprintf(
		render.body,
		"%sif err := %s; err != nil { return nil, err }\n",
		indent,
		call,
	)
}

// emitWriteError 生成字段写入错误分支，并在请求路径归还尚未提交的 Buffer。
func (render *renderer) emitWriteError(indent, call string) {
	if render.response {
		fmt.Fprintf(
			render.body,
			"%sif err := %s; err != nil { return err }\n",
			indent,
			call,
		)
		return
	}
	fmt.Fprintf(
		render.body,
		"%sif err := %s; err != nil { buffer.Release(); return nil, err }\n",
		indent,
		call,
	)
}

// emitSize 生成一个值的静态大小计算；topLevel 决定是否使用官方 Protobuf Codec。
func (render *renderer) emitSize(
	expression string,
	typ types.Type,
	topLevel bool,
	indent string,
) {
	// 自定义 Codec 对精确具名值具有最高优先级；外层指针和容器会在各自递归分支
	// 先写入 M11 的 presence 或数量，再到达这里选择元素 Codec。
	if codec := render.codecs.lookup(typ); codec != nil {
		render.emitCustomSize(expression, codec, indent)
		return
	}
	if topLevel && isProtoType(typ) {
		render.emitProtoSize(expression, typ, indent)
		return
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		if value.Kind() == types.String {
			render.emitCodecError(
				indent,
				fmt.Sprintf("sizer.AddString(string(%s))", expression),
			)
			return
		}
		render.emitCodecError(
			indent,
			fmt.Sprintf("sizer.Add(%d)", minEncodedSize(typ, false)),
		)
	case *types.Pointer:
		render.emitCodecError(indent, "sizer.Add(1)")
		fmt.Fprintf(render.body, "%sif %s != nil {\n", indent, expression)
		render.emitSize("(*"+expression+")", value.Elem(), false, indent+"\t")
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Array:
		index := render.next("index")
		fmt.Fprintf(
			render.body,
			"%sfor %s := range %s {\n",
			indent,
			index,
			expression,
		)
		render.emitSize(
			fmt.Sprintf("%s[%s]", expression, index),
			value.Elem(),
			false,
			indent+"\t",
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Slice:
		if isByteSlice(typ) {
			render.emitCodecError(
				indent,
				fmt.Sprintf("sizer.AddBytes([]byte(%s))", expression),
			)
			return
		}
		render.emitCodecError(
			indent,
			fmt.Sprintf(
				"sizer.AddContainer(len(%s), %s == nil)",
				expression,
				expression,
			),
		)
		index := render.next("index")
		fmt.Fprintf(
			render.body,
			"%sfor %s := range %s {\n",
			indent,
			index,
			expression,
		)
		render.emitSize(
			fmt.Sprintf("%s[%s]", expression, index),
			value.Elem(),
			false,
			indent+"\t",
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Map:
		render.emitCodecError(
			indent,
			fmt.Sprintf(
				"sizer.AddContainer(len(%s), %s == nil)",
				expression,
				expression,
			),
		)
		key := render.next("key")
		item := render.next("value")
		fmt.Fprintf(
			render.body,
			"%sfor %s, %s := range %s {\n",
			indent,
			key,
			item,
			expression,
		)
		render.emitSize(key, value.Key(), false, indent+"\t")
		render.emitSize(item, value.Elem(), false, indent+"\t")
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Struct:
		for _, field := range exportedFields(typ) {
			render.emitSize(
				expression+"."+field.Name(),
				field.Type(),
				false,
				indent,
			)
		}
	}
}

// emitCustomSize 生成具体 Provider 的 Size 直接调用和统一编码错误映射。
func (render *renderer) emitCustomSize(
	expression string,
	codec *customCodec,
	indent string,
) {
	size := render.next("customSize")
	codecErr := render.next("customErr")
	fmt.Fprintf(
		render.body,
		"%s%s, %s := %s{}.Size(&(%s))\n",
		indent,
		size,
		codecErr,
		render.imports.typeName(codec.provider),
		expression,
	)
	render.emitCustomEncodeFailure(indent, codecErr+" != nil", false)
	render.emitCodecError(
		indent,
		fmt.Sprintf("sizer.AddCustom(%s)", size),
	)
}

func (render *renderer) emitProtoSize(
	expression string,
	typ types.Type,
	indent string,
) {
	// 指针消息用四字节 nil 标记区分空消息；值消息始终存在并静态取址。
	if pointer, ok := typ.(*types.Pointer); ok {
		_ = pointer
		fmt.Fprintf(render.body, "%sif %s == nil {\n", indent, expression)
		render.emitCodecError(indent+"\t", "sizer.Add(4)")
		fmt.Fprintf(render.body, "%s} else {\n", indent)
		render.emitCodecError(
			indent+"\t",
			fmt.Sprintf("sizer.AddProto(%s)", expression),
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
		return
	}
	render.emitCodecError(
		indent,
		fmt.Sprintf("sizer.AddProto(&%s)", expression),
	)
}

// emitWrite 递归生成一个值的静态写入步骤；topLevel 决定 Protobuf 是否走官方 Codec。
func (render *renderer) emitWrite(
	expression string,
	typ types.Type,
	topLevel bool,
	indent string,
) {
	if codec := render.codecs.lookup(typ); codec != nil {
		render.emitCustomWrite(expression, codec, indent)
		return
	}
	if topLevel && isProtoType(typ) {
		if _, pointer := typ.(*types.Pointer); pointer {
			fmt.Fprintf(render.body, "%sif %s == nil {\n", indent, expression)
			render.emitWriteError(indent+"\t", "writer.WriteNil()")
			fmt.Fprintf(render.body, "%s} else {\n", indent)
			render.emitWriteError(
				indent+"\t",
				fmt.Sprintf("writer.WriteProto(%s)", expression),
			)
			fmt.Fprintf(render.body, "%s}\n", indent)
		} else {
			render.emitWriteError(
				indent,
				fmt.Sprintf("writer.WriteProto(&%s)", expression),
			)
		}
		return
	}
	switch value := typ.Underlying().(type) {
	case *types.Basic:
		suffix := basicMethodSuffix(typ)
		cast := basicCast(value.Kind(), expression)
		render.emitWriteError(
			indent,
			fmt.Sprintf("writer.Write%s(%s)", suffix, cast),
		)
	case *types.Pointer:
		render.emitWriteError(
			indent,
			fmt.Sprintf("writer.WritePresence(%s != nil)", expression),
		)
		fmt.Fprintf(render.body, "%sif %s != nil {\n", indent, expression)
		render.emitWrite("(*"+expression+")", value.Elem(), false, indent+"\t")
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Array:
		index := render.next("index")
		fmt.Fprintf(render.body, "%sfor %s := range %s {\n", indent, index, expression)
		render.emitWrite(
			fmt.Sprintf("%s[%s]", expression, index),
			value.Elem(),
			false,
			indent+"\t",
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Slice:
		if isByteSlice(typ) {
			render.emitWriteError(
				indent,
				fmt.Sprintf("writer.WriteBytes([]byte(%s))", expression),
			)
			return
		}
		render.emitWriteError(
			indent,
			fmt.Sprintf(
				"writer.WriteContainer(len(%s), %s == nil)",
				expression,
				expression,
			),
		)
		index := render.next("index")
		fmt.Fprintf(render.body, "%sfor %s := range %s {\n", indent, index, expression)
		render.emitWrite(
			fmt.Sprintf("%s[%s]", expression, index),
			value.Elem(),
			false,
			indent+"\t",
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Map:
		render.emitWriteError(
			indent,
			fmt.Sprintf(
				"writer.WriteContainer(len(%s), %s == nil)",
				expression,
				expression,
			),
		)
		key := render.next("key")
		item := render.next("value")
		fmt.Fprintf(
			render.body,
			"%sfor %s, %s := range %s {\n",
			indent,
			key,
			item,
			expression,
		)
		render.emitWrite(key, value.Key(), false, indent+"\t")
		render.emitWrite(item, value.Elem(), false, indent+"\t")
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Struct:
		for _, field := range exportedFields(typ) {
			render.emitWrite(
				expression+"."+field.Name(),
				field.Type(),
				false,
				indent,
			)
		}
	}
}

// emitCustomWrite 再次取得当前值准确长度，并让 Provider 直接覆盖最终 Buffer 区域。
func (render *renderer) emitCustomWrite(
	expression string,
	codec *customCodec,
	indent string,
) {
	provider := render.imports.typeName(codec.provider) + "{}"
	size := render.next("customSize")
	codecErr := render.next("customErr")
	fmt.Fprintf(
		render.body,
		"%s%s, %s := %s.Size(&(%s))\n",
		indent,
		size,
		codecErr,
		provider,
		expression,
	)
	render.emitCustomEncodeFailure(indent, codecErr+" != nil", true)

	payload := render.next("customPayload")
	reserveErr := render.next("reserveErr")
	fmt.Fprintf(
		render.body,
		"%s%s, %s := writer.ReserveCustom(%s)\n",
		indent,
		payload,
		reserveErr,
		size,
	)
	render.emitCustomWriteError(indent, reserveErr)

	written := render.next("customWritten")
	marshalErr := render.next("customErr")
	fmt.Fprintf(
		render.body,
		"%s%s, %s := %s.MarshalTo(%s, &(%s))\n",
		indent,
		written,
		marshalErr,
		provider,
		payload,
		expression,
	)
	render.emitCustomEncodeFailure(
		indent,
		fmt.Sprintf(
			"%s != nil || %s != len(%s)",
			marshalErr,
			written,
			payload,
		),
		true,
	)
}

// emitCustomEncodeFailure 生成固定 CodeRPCEncodeFailed，并按请求所有权决定是否释放 Buffer。
func (render *renderer) emitCustomEncodeFailure(
	indent string,
	condition string,
	bufferAllocated bool,
) {
	fmt.Fprintf(render.body, "%sif %s {\n", indent, condition)
	if render.response {
		fmt.Fprintf(
			render.body,
			"%s\treturn %s.ErrRPCEncodeFailed\n",
			indent,
			render.errsAlias,
		)
	} else if bufferAllocated {
		fmt.Fprintf(
			render.body,
			"%s\tbuffer.Release()\n%s\treturn nil, %s.ErrRPCEncodeFailed\n",
			indent,
			indent,
			render.errsAlias,
		)
	} else {
		fmt.Fprintf(
			render.body,
			"%s\treturn nil, %s.ErrRPCEncodeFailed\n",
			indent,
			render.errsAlias,
		)
	}
	fmt.Fprintf(render.body, "%s}\n", indent)
}

// emitCustomWriteError 处理已经取得请求 Buffer 后的 Writer 长度失败。
func (render *renderer) emitCustomWriteError(indent, errName string) {
	fmt.Fprintf(render.body, "%sif %s != nil {\n", indent, errName)
	if render.response {
		fmt.Fprintf(render.body, "%s\treturn %s\n", indent, errName)
	} else {
		fmt.Fprintf(
			render.body,
			"%s\tbuffer.Release()\n%s\treturn nil, %s\n",
			indent,
			indent,
			errName,
		)
	}
	fmt.Fprintf(render.body, "%s}\n", indent)
}

// emitRead 把 Reader 结果写入已经声明的目标表达式。
func (render *renderer) emitRead(
	target string,
	typ types.Type,
	topLevel bool,
	response bool,
	indent string,
) {
	failureReturn := "return"
	readerKind := "false"
	if response {
		readerKind = "true"
	}
	if codec := render.codecs.lookup(typ); codec != nil {
		render.emitCustomRead(target, codec, indent)
		return
	}
	if topLevel && isProtoType(typ) {
		payload := render.next("payload")
		isNil := render.next("isNil")
		fmt.Fprintf(
			render.body,
			"%svar %s []byte\n%svar %s bool\n"+
				"%s%s, %s, decodeErr = reader.ReadProtoPayload()\n",
			indent,
			payload,
			indent,
			isNil,
			indent,
			payload,
			isNil,
		)
		fmt.Fprintf(render.body, "%sif decodeErr != nil { %s }\n", indent, failureReturn)
		if pointer, ok := typ.(*types.Pointer); ok {
			fmt.Fprintf(render.body, "%sif !%s {\n", indent, isNil)
			fmt.Fprintf(
				render.body,
				"%s\t%s = new(%s)\n",
				indent,
				target,
				render.imports.typeName(pointer.Elem()),
			)
			fmt.Fprintf(
				render.body,
				"%s\tdecodeErr = %s.UnmarshalProto(%s, %s, %s)\n",
				indent,
				render.rpcAlias,
				payload,
				target,
				readerKind,
			)
			fmt.Fprintf(render.body, "%s\tif decodeErr != nil { return }\n", indent)
			fmt.Fprintf(render.body, "%s}\n", indent)
		} else {
			fmt.Fprintf(
				render.body,
				"%sif %s { decodeErr = %s; return }\n",
				indent,
				isNil,
				decodeErrorName(response),
			)
			fmt.Fprintf(
				render.body,
				"%sdecodeErr = %s.UnmarshalProto(%s, &%s, %s)\n",
				indent,
				render.rpcAlias,
				payload,
				target,
				readerKind,
			)
			fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
		}
		return
	}

	switch value := typ.Underlying().(type) {
	case *types.Basic:
		suffix := basicMethodSuffix(typ)
		temporary := render.next("decoded")
		fmt.Fprintf(
			render.body,
			"%svar %s %s\n%s%s, decodeErr = reader.Read%s()\n",
			indent,
			temporary,
			render.imports.typeName(typ.Underlying()),
			indent,
			temporary,
			suffix,
		)
		fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
		fmt.Fprintf(
			render.body,
			"%s%s = %s(%s)\n",
			indent,
			target,
			render.imports.typeName(typ),
			temporary,
		)
	case *types.Pointer:
		present := render.next("present")
		fmt.Fprintf(
			render.body,
			"%svar %s bool\n%s%s, decodeErr = reader.ReadPresence()\n",
			indent,
			present,
			indent,
			present,
		)
		fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
		fmt.Fprintf(render.body, "%sif %s {\n", indent, present)
		fmt.Fprintf(
			render.body,
			"%s\t%s = (%s)(new(%s))\n",
			indent,
			target,
			render.imports.typeName(typ),
			render.imports.typeName(value.Elem()),
		)
		render.emitRead("(*"+target+")", value.Elem(), false, response, indent+"\t")
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Array:
		index := render.next("index")
		fmt.Fprintf(render.body, "%sfor %s := range %s {\n", indent, index, target)
		render.emitRead(
			fmt.Sprintf("%s[%s]", target, index),
			value.Elem(),
			false,
			response,
			indent+"\t",
		)
		fmt.Fprintf(render.body, "%s}\n", indent)
	case *types.Slice:
		if isByteSlice(typ) {
			temporary := render.next("decoded")
			fmt.Fprintf(
				render.body,
				"%svar %s []byte\n%s%s, decodeErr = reader.ReadBytes()\n",
				indent,
				temporary,
				indent,
				temporary,
			)
			fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
			fmt.Fprintf(
				render.body,
				"%s%s = %s(%s)\n",
				indent,
				target,
				render.imports.typeName(typ),
				temporary,
			)
			return
		}
		length, isNil := render.emitContainerHeader(target, typ, response, indent)
		fmt.Fprintf(render.body, "%sif !%s {\n", indent, isNil)
		fmt.Fprintf(
			render.body,
			"%s\t%s = make(%s, %s)\n",
			indent,
			target,
			render.imports.typeName(typ),
			length,
		)
		index := render.next("index")
		fmt.Fprintf(
			render.body,
			"%s\tfor %s := 0; %s < %s; %s++ {\n",
			indent,
			index,
			index,
			length,
			index,
		)
		render.emitRead(
			fmt.Sprintf("%s[%s]", target, index),
			value.Elem(),
			false,
			response,
			indent+"\t\t",
		)
		fmt.Fprintf(render.body, "%s\t}\n%s}\n", indent, indent)
	case *types.Map:
		length, isNil := render.emitContainerHeader(target, typ, response, indent)
		fmt.Fprintf(render.body, "%sif !%s {\n", indent, isNil)
		fmt.Fprintf(
			render.body,
			"%s\t%s = make(%s, %s)\n",
			indent,
			target,
			render.imports.typeName(typ),
			length,
		)
		index := render.next("index")
		key := render.next("key")
		item := render.next("value")
		fmt.Fprintf(
			render.body,
			"%s\tfor %s := 0; %s < %s; %s++ {\n",
			indent,
			index,
			index,
			length,
			index,
		)
		fmt.Fprintf(
			render.body,
			"%s\t\tvar %s %s\n",
			indent,
			key,
			render.imports.typeName(value.Key()),
		)
		fmt.Fprintf(
			render.body,
			"%s\t\tvar %s %s\n",
			indent,
			item,
			render.imports.typeName(value.Elem()),
		)
		render.emitRead(key, value.Key(), false, response, indent+"\t\t")
		render.emitRead(item, value.Elem(), false, response, indent+"\t\t")
		fmt.Fprintf(
			render.body,
			"%s\t\t%s[%s] = %s\n",
			indent,
			target,
			key,
			item,
		)
		fmt.Fprintf(render.body, "%s\t}\n%s}\n", indent, indent)
	case *types.Struct:
		for _, field := range exportedFields(typ) {
			render.emitRead(
				target+"."+field.Name(),
				field.Type(),
				false,
				response,
				indent,
			)
		}
	}
}

// emitCustomRead 读取准确边界，并把 Provider error 映射成 Reader 固定的请求或响应错误。
func (render *renderer) emitCustomRead(
	target string,
	codec *customCodec,
	indent string,
) {
	payload := render.next("customPayload")
	fmt.Fprintf(
		render.body,
		"%svar %s []byte\n%s%s, decodeErr = reader.ReadCustomPayload()\n",
		indent,
		payload,
		indent,
		payload,
	)
	fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
	codecErr := render.next("customErr")
	fmt.Fprintf(
		render.body,
		"%s%s := %s{}.Unmarshal(%s, &(%s))\n",
		indent,
		codecErr,
		render.imports.typeName(codec.provider),
		payload,
		target,
	)
	fmt.Fprintf(render.body, "%sif %s != nil {\n", indent, codecErr)
	fmt.Fprintf(
		render.body,
		"%s\tdecodeErr = reader.Reject()\n%s\treturn\n%s}\n",
		indent,
		indent,
		indent,
	)
}

// emitContainerHeader 生成容器数量、nil 语义和分配前最小载荷检查。
func (render *renderer) emitContainerHeader(
	target string,
	typ types.Type,
	response bool,
	indent string,
) (length string, isNil string) {
	length = render.next("length")
	isNil = render.next("isNil")
	fmt.Fprintf(
		render.body,
		"%svar %s int\n%svar %s bool\n"+
			"%s%s, %s, decodeErr = reader.ReadContainer()\n",
		indent,
		length,
		indent,
		isNil,
		indent,
		length,
		isNil,
	)
	fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
	fmt.Fprintf(
		render.body,
		"%sdecodeErr = reader.CheckElements(%s, %d)\n",
		indent,
		length,
		containerMinimumSize(typ, render.codecs),
	)
	fmt.Fprintf(render.body, "%sif decodeErr != nil { return }\n", indent)
	_ = target
	_ = response
	return length, isNil
}

// containerMinimumSize 返回 Slice 元素或 Map 键值对不可能低于的线格式大小。
func containerMinimumSize(
	typ types.Type,
	codecs *codecRegistry,
) int {
	switch value := typ.Underlying().(type) {
	case *types.Slice:
		return minEncodedSizeWithCodecs(value.Elem(), false, codecs)
	case *types.Map:
		return minEncodedSizeWithCodecs(value.Key(), false, codecs) +
			minEncodedSizeWithCodecs(value.Elem(), false, codecs)
	default:
		panic("rpcgen: container element requested for non-container")
	}
}

// basicCast 把具名基础类型显式转换成 Writer 对应的标准 Go 参数类型。
func basicCast(kind types.BasicKind, expression string) string {
	switch kind {
	case types.Bool:
		return "bool(" + expression + ")"
	case types.Int:
		return "int(" + expression + ")"
	case types.Int8:
		return "int8(" + expression + ")"
	case types.Int16:
		return "int16(" + expression + ")"
	case types.Int32:
		return "int32(" + expression + ")"
	case types.Int64:
		return "int64(" + expression + ")"
	case types.Uint:
		return "uint(" + expression + ")"
	case types.Uint8:
		return "uint8(" + expression + ")"
	case types.Uint16:
		return "uint16(" + expression + ")"
	case types.Uint32:
		return "uint32(" + expression + ")"
	case types.Uint64:
		return "uint64(" + expression + ")"
	case types.Float32:
		return "float32(" + expression + ")"
	case types.Float64:
		return "float64(" + expression + ")"
	case types.String:
		return "string(" + expression + ")"
	default:
		panic("rpcgen: unsupported cast")
	}
}

// decodeErrorName 返回生成代码在请求或响应 nil Protobuf 位置使用的稳定哨兵名称。
func decodeErrorName(response bool) string {
	if response {
		return "errs.ErrRPCResponseDecodeFailed"
	}
	return "errs.ErrRPCRequestDecodeFailed"
}
