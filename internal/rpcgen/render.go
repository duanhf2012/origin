package rpcgen

import (
	"bytes"
	"fmt"
	"strings"
)

// renderer 在一个方法内生成唯一局部变量名和静态 Codec 步骤。
type renderer struct {
	body       *bytes.Buffer
	imports    *importSet
	rpcAlias   string
	errsAlias  string
	protoAlias string
	codecs     *codecRegistry
	counter    int
	response   bool
}

// renderContract 依次生成稳定标识、客户端、静态 Codec 和 Dispatcher。
func renderContract(
	body *bytes.Buffer,
	imports *importSet,
	contextAlias string,
	rpcAlias string,
	item *contract,
) error {
	fingerprintLiteral := byteArrayLiteral(item.fingerprint[:])
	contractPrefix := lowerFirst(item.name)
	serviceAlias := imports.add(
		"github.com/duanhf2012/origin/v3/service",
		"service",
	)
	// 生成体内多处直接使用 errs。先于业务类型导入保留别名，确保契约恰好引用一个
	// 包名为 errs 的业务类型时，该业务包自动获得 errs2，而不会破坏生成代码。
	errsAlias := imports.add("github.com/duanhf2012/origin/v3/errs", "errs")
	fmt.Fprintf(
		body,
		"const %sContractID %s.ContractID = 0x%016x\n\n"+
			"var %sFingerprint = %s.ContractFingerprint%s\n\n",
		contractPrefix,
		rpcAlias,
		item.id,
		contractPrefix,
		rpcAlias,
		fingerprintLiteral,
	)
	for _, candidate := range item.methods {
		fmt.Fprintf(
			body,
			"const %s%sMethodID %s.MethodID = 0x%016x\n",
			contractPrefix,
			candidate.name,
			rpcAlias,
			candidate.id,
		)
	}
	body.WriteByte('\n')

	clientName := item.name + "Client"
	fmt.Fprintf(
		body,
		"// %s 是 %s 的强类型生成客户端。\n"+
			"type %s struct {\n\tclient %s.Client\n}\n\n"+
			"// New%s 创建绑定 owner 和逻辑目标的轻量客户端。\n"+
			"func New%s(owner %s.IService, target %s.Target) %s {\n"+
			"\treturn %s{client: %s.NewGeneratedClient(owner, target, %sContractID, %sFingerprint)}\n"+
			"}\n\n",
		clientName,
		item.fullName,
		clientName,
		rpcAlias,
		clientName,
		clientName,
		serviceAlias,
		rpcAlias,
		clientName,
		clientName,
		rpcAlias,
		contractPrefix,
		contractPrefix,
	)
	defaultName := defaultServiceName(item.name)
	fmt.Fprintf(
		body,
		"// Bind%s 使用契约默认 ServiceName %q 绑定轻量客户端。\n"+
			"func Bind%s(owner %s.IService) %s {\n"+
			"\treturn New%s(owner, %s.ToService(%q))\n"+
			"}\n\n"+
			"// Bind%sTo 使用实际 ServiceName 绑定模板改名后的轻量客户端。\n"+
			"func Bind%sTo(owner %s.IService, serviceName string) %s {\n"+
			"\treturn New%s(owner, %s.ToService(serviceName))\n"+
			"}\n\n"+
			"// OnNode 保留已绑定 ServiceName 并把目标收窄到指定 Node。\n"+
			"func (client %s) OnNode(nodeID string) %s {\n"+
			"\tclient.client = client.client.OnNode(nodeID)\n"+
			"\treturn client\n"+
			"}\n\n"+
			"// RouteRoundRobin 派生显式轮询路由客户端。\n"+
			"func (client %s) RouteRoundRobin() %s {\n"+
			"\tclient.client = client.client.RouteRoundRobin()\n"+
			"\treturn client\n"+
			"}\n\n"+
			"// RouteRandom 派生随机路由客户端。\n"+
			"func (client %s) RouteRandom() %s {\n"+
			"\tclient.client = client.client.RouteRandom()\n"+
			"\treturn client\n"+
			"}\n\n"+
			"// Route 派生稳定业务 Key 路由客户端。\n"+
			"func (client %s) Route(key any) %s {\n"+
			"\tclient.client = client.client.Route(key)\n"+
			"\treturn client\n"+
			"}\n\n"+
			"// RouteBy 派生自定义 Selector 路由客户端。\n"+
			"func (client %s) RouteBy(selector %s.RouteSelector) %s {\n"+
			"\tclient.client = client.client.RouteBy(selector)\n"+
			"\treturn client\n"+
			"}\n\n",
		item.name,
		defaultName,
		item.name,
		serviceAlias,
		clientName,
		clientName,
		rpcAlias,
		defaultName,
		item.name,
		item.name,
		serviceAlias,
		clientName,
		clientName,
		rpcAlias,
		clientName,
		clientName,
		clientName,
		clientName,
		clientName,
		clientName,
		clientName,
		clientName,
		clientName,
		rpcAlias,
		clientName,
	)
	fmt.Fprintf(
		body,
		"// IncludeRetired 派生在自动范围中同时接受 Running 和 Retired 的客户端。\n"+
			"func (client %s) IncludeRetired() %s {\n"+
			"\tclient.client = client.client.IncludeRetired()\n"+
			"\treturn client\n"+
			"}\n\n",
		clientName,
		clientName,
	)
	for _, candidate := range item.methods {
		renderCodecFunctions(
			body,
			imports,
			rpcAlias,
			errsAlias,
			item,
			candidate,
		)
		renderClientMethods(
			body,
			imports,
			contextAlias,
			rpcAlias,
			clientName,
			contractPrefix,
			candidate,
		)
	}
	renderDispatcher(body, imports, contextAlias, rpcAlias, item, contractPrefix)
	return nil
}

func defaultServiceName(contractName string) string {
	if strings.HasSuffix(contractName, "RPC") &&
		len(contractName) > len("RPC") {
		return strings.TrimSuffix(contractName, "RPC") + "Service"
	}
	return contractName + "Service"
}

// renderCodecFunctions 为一个方法生成请求编解码，并按调用分类决定是否生成响应编解码。
func renderCodecFunctions(
	body *bytes.Buffer,
	imports *importSet,
	rpcAlias string,
	errsAlias string,
	item *contract,
	candidate *method,
) {
	prefix := lowerFirst(item.name) + candidate.name
	renderEncode(
		body,
		imports,
		rpcAlias,
		"encode"+upperFirst(prefix)+"Request",
		candidate.inputs,
		true,
		item.codecs,
		errsAlias,
	)
	renderDecode(
		body,
		imports,
		rpcAlias,
		"decode"+upperFirst(prefix)+"Request",
		candidate.inputs,
		true,
		item.codecs,
		errsAlias,
	)
	if !candidate.notifyOnly {
		renderResponseEncode(
			body,
			imports,
			rpcAlias,
			"encode"+upperFirst(prefix)+"Response",
			candidate.outputs,
			item.codecs,
			errsAlias,
		)
		renderDecode(
			body,
			imports,
			rpcAlias,
			"decode"+upperFirst(prefix)+"Response",
			candidate.outputs,
			false,
			item.codecs,
			errsAlias,
		)
	}
}

// renderEncode 生成请求的精确大小计算和一次最终 Buffer 写入。
func renderEncode(
	body *bytes.Buffer,
	imports *importSet,
	rpcAlias string,
	name string,
	parameters []parameter,
	request bool,
	codecs *codecRegistry,
	errsAlias string,
) {
	fmt.Fprintf(
		body,
		"// %s 计算并一次写入当前方法请求载荷。\n",
		name,
	)
	fmt.Fprintf(
		body,
		"func %s(client %s.Client, kind %s.CallKind",
		name,
		rpcAlias,
		rpcAlias,
	)
	for _, parameter := range parameters {
		fmt.Fprintf(body, ", %s %s", parameter.name, imports.typeName(parameter.typ))
	}
	fmt.Fprintf(body, ") (*%s.Buffer, error) {\n", rpcAlias)
	fmt.Fprintf(body, "\tsizer := %s.NewSizer()\n", rpcAlias)
	sizeRenderer := &renderer{
		body:      body,
		imports:   imports,
		rpcAlias:  rpcAlias,
		errsAlias: errsAlias,
		codecs:    codecs,
	}
	for _, parameter := range parameters {
		sizeRenderer.emitSize(parameter.name, parameter.typ, true, "\t")
	}
	body.WriteString("\tsize, err := sizer.Size()\n")
	body.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	body.WriteString("\tbuffer, err := client.AllocateRequest(size, kind)\n")
	body.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
	fmt.Fprintf(body, "\twriter := %s.NewWriter(buffer.Bytes())\n", rpcAlias)
	writeRenderer := &renderer{
		body:      body,
		imports:   imports,
		rpcAlias:  rpcAlias,
		errsAlias: errsAlias,
		codecs:    codecs,
		counter:   sizeRenderer.counter,
	}
	for _, parameter := range parameters {
		writeRenderer.emitWrite(parameter.name, parameter.typ, true, "\t")
	}
	body.WriteString("\tif err := writer.Done(); err != nil {\n")
	body.WriteString("\t\tbuffer.Release()\n\t\treturn nil, err\n\t}\n")
	body.WriteString("\treturn buffer, nil\n}\n\n")
	_ = request
}

// renderResponseEncode 生成 Dispatcher 使用 ResponseWriter 的响应编码器。
func renderResponseEncode(
	body *bytes.Buffer,
	imports *importSet,
	rpcAlias string,
	name string,
	parameters []parameter,
	codecs *codecRegistry,
	errsAlias string,
) {
	fmt.Fprintf(
		body,
		"// %s 计算并一次写入当前方法响应载荷。\n",
		name,
	)
	fmt.Fprintf(body, "func %s(response *%s.ResponseWriter", name, rpcAlias)
	for _, parameter := range parameters {
		fmt.Fprintf(body, ", %s %s", parameter.name, imports.typeName(parameter.typ))
	}
	body.WriteString(") error {\n")
	fmt.Fprintf(body, "\tsizer := %s.NewSizer()\n", rpcAlias)
	sizeRenderer := &renderer{
		body:      body,
		imports:   imports,
		rpcAlias:  rpcAlias,
		errsAlias: errsAlias,
		codecs:    codecs,
		response:  true,
	}
	for _, parameter := range parameters {
		sizeRenderer.emitSize(parameter.name, parameter.typ, true, "\t")
	}
	body.WriteString("\tsize, err := sizer.Size()\n")
	body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
	body.WriteString("\ttarget, err := response.Allocate(size)\n")
	body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
	fmt.Fprintf(body, "\twriter := %s.NewWriter(target)\n", rpcAlias)
	writeRenderer := &renderer{
		body:      body,
		imports:   imports,
		rpcAlias:  rpcAlias,
		errsAlias: errsAlias,
		codecs:    codecs,
		counter:   sizeRenderer.counter,
		response:  true,
	}
	for _, parameter := range parameters {
		writeRenderer.emitWrite(parameter.name, parameter.typ, true, "\t")
	}
	body.WriteString("\treturn writer.Done()\n}\n\n")
}

func renderDecode(
	body *bytes.Buffer,
	imports *importSet,
	rpcAlias string,
	name string,
	parameters []parameter,
	request bool,
	codecs *codecRegistry,
	errsAlias string,
) {
	fmt.Fprintf(
		body,
		"// %s 按固定契约顺序解码并校验完整载荷。\n",
		name,
	)
	fmt.Fprintf(body, "func %s(data []byte) (", name)
	for _, parameter := range parameters {
		fmt.Fprintf(body, "%s %s, ", parameter.name, imports.typeName(parameter.typ))
	}
	body.WriteString("decodeErr error) {\n")
	if request {
		fmt.Fprintf(body, "\treader := %s.NewRequestReader(data)\n", rpcAlias)
	} else {
		fmt.Fprintf(body, "\treader := %s.NewResponseReader(data)\n", rpcAlias)
	}
	decodeRenderer := &renderer{
		body:      body,
		imports:   imports,
		rpcAlias:  rpcAlias,
		errsAlias: errsAlias,
		codecs:    codecs,
	}
	for _, parameter := range parameters {
		decodeRenderer.emitRead(parameter.name, parameter.typ, true, !request, "\t")
	}
	body.WriteString("\tdecodeErr = reader.Done()\n\treturn\n}\n\n")
}

func renderClientMethods(
	body *bytes.Buffer,
	imports *importSet,
	contextAlias string,
	rpcAlias string,
	clientName string,
	contractPrefix string,
	candidate *method,
) {
	encodeName := "encode" + upperFirst(contractPrefix+candidate.name) + "Request"
	decodeName := "decode" + upperFirst(contractPrefix+candidate.name) + "Response"
	methodID := contractPrefix + candidate.name + "MethodID"
	inputDecl := parameterDeclarations(imports, candidate.inputs)
	inputNames := parameterNames(candidate.inputs)

	if !candidate.notifyOnly {
		// Await 外观。
		fmt.Fprintf(
			body,
			"// Await%s 以顺序编程外观等待 RPC 结果。\n",
			candidate.name,
		)
		fmt.Fprintf(
			body,
			"func (client %s) Await%s(ctx %s.Context%s) (",
			clientName,
			candidate.name,
			contextAlias,
			inputDecl,
		)
		for _, output := range candidate.outputs {
			fmt.Fprintf(body, "%s %s, ", output.name, imports.typeName(output.typ))
		}
		body.WriteString("err error) {\n")
		fmt.Fprintf(
			body,
			"\tpreparedClient, err := client.client.PrepareAwait(ctx, %s)\n",
			methodID,
		)
		body.WriteString("\tif err != nil {\n\t\treturn\n\t}\n")
		fmt.Fprintf(
			body,
			"\trequest, err := %s(preparedClient, %s.CallRequest%s)\n",
			encodeName,
			rpcAlias,
			inputNames,
		)
		body.WriteString("\tif err != nil {\n\t\treturn\n\t}\n")
		fmt.Fprintf(
			body,
			"\terr = preparedClient.Await(ctx, %s, request, func(data []byte) error {\n",
			methodID,
		)
		if len(candidate.outputs) == 0 {
			fmt.Fprintf(body, "\t\treturn %s(data)\n", decodeName)
		} else {
			body.WriteString("\t\t")
			for index, output := range candidate.outputs {
				if index > 0 {
					body.WriteString(", ")
				}
				body.WriteString(output.name)
			}
			fmt.Fprintf(body, ", err = %s(data)\n\t\treturn err\n", decodeName)
		}
		body.WriteString("\t})\n\treturn\n}\n\n")

		// Async 外观。
		fmt.Fprintf(
			body,
			"// Async%s 提交请求，并在 owner 的后续串行任务中执行一次回调。\n",
			candidate.name,
		)
		fmt.Fprintf(
			body,
			"func (client %s) Async%s(ctx %s.Context%s, callback func(%s.Context",
			clientName,
			candidate.name,
			contextAlias,
			inputDecl,
			contextAlias,
		)
		for _, output := range candidate.outputs {
			fmt.Fprintf(body, ", %s", imports.typeName(output.typ))
		}
		body.WriteString(", error)) error {\n")
		// 生成闭包本身始终非 nil；必须在编码和提交前检查业务 callback，不能把 nil
		// 延迟到 Service 工作任务中才形成 panic。
		body.WriteString("\tif callback == nil {\n\t\treturn errs.ErrInvalidArgument\n\t}\n")
		fmt.Fprintf(
			body,
			"\tpreparedClient, err := client.client.PrepareAsync(ctx, %s)\n",
			methodID,
		)
		body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		fmt.Fprintf(
			body,
			"\trequest, err := %s(preparedClient, %s.CallRequest%s)\n",
			encodeName,
			rpcAlias,
			inputNames,
		)
		body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		fmt.Fprintf(
			body,
			"\treturn preparedClient.Async(ctx, %s, request, func(callbackCtx %s.Context, data []byte, callErr error) {\n",
			methodID,
			contextAlias,
		)
		body.WriteString("\t\tif callErr != nil {\n\t\t\tcallback(callbackCtx")
		for _, output := range candidate.outputs {
			fmt.Fprintf(body, ", *new(%s)", imports.typeName(output.typ))
		}
		body.WriteString(", callErr)\n\t\t\treturn\n\t\t}\n")
		if len(candidate.outputs) == 0 {
			fmt.Fprintf(body, "\t\tdecodeErr := %s(data)\n", decodeName)
			body.WriteString("\t\tcallback(callbackCtx, decodeErr)\n")
		} else {
			body.WriteString("\t\t")
			for index, output := range candidate.outputs {
				if index > 0 {
					body.WriteString(", ")
				}
				body.WriteString(output.name)
			}
			fmt.Fprintf(body, ", decodeErr := %s(data)\n", decodeName)
			body.WriteString("\t\tcallback(callbackCtx")
			for _, output := range candidate.outputs {
				body.WriteString(", " + output.name)
			}
			body.WriteString(", decodeErr)\n")
		}
		body.WriteString("\t})\n}\n\n")
	}

	// Notify 与 Broadcast 对所有方法统一生成。
	for _, prefix := range []string{"Notify", "Broadcast"} {
		fmt.Fprintf(
			body,
			"// %s%s 提交通知并主动放弃业务结果。\n",
			prefix,
			candidate.name,
		)
		fmt.Fprintf(
			body,
			"func (client %s) %s%s(ctx %s.Context%s) error {\n",
			clientName,
			prefix,
			candidate.name,
			contextAlias,
			inputDecl,
		)
		prepareMethod := "PrepareNotify"
		if prefix == "Broadcast" {
			prepareMethod = "PrepareBroadcast"
		}
		fmt.Fprintf(
			body,
			"\tpreparedClient, err := client.client.%s(ctx, %s)\n",
			prepareMethod,
			methodID,
		)
		body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		clientExpression := "preparedClient"
		fmt.Fprintf(
			body,
			"\trequest, err := %s(%s, %s.CallNotify%s)\n",
			encodeName,
			clientExpression,
			rpcAlias,
			inputNames,
		)
		body.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		fmt.Fprintf(
			body,
			"\treturn %s.%s(ctx, %s, request)\n}\n\n",
			clientExpression,
			prefix,
			methodID,
		)
	}
}

func renderDispatcher(
	body *bytes.Buffer,
	imports *importSet,
	contextAlias string,
	rpcAlias string,
	item *contract,
	contractPrefix string,
) {
	name := lowerFirst(item.name) + "Dispatcher"
	fmt.Fprintf(
		body,
		"// %s 把 MethodID 静态分派到 %s 实现。\n"+
			"type %s struct { impl %s }\n\n"+
			"// New%sDispatcher 创建不使用反射的静态 Dispatcher。\n"+
			"func New%sDispatcher(impl %s) %s.Dispatcher {\n"+
			"\treturn &%s{impl: impl}\n"+
			"}\n\n"+
			"// ContractID 返回生成期冻结的契约标识。\n"+
			"func (dispatcher *%s) ContractID() %s.ContractID { return %sContractID }\n\n"+
			"// Fingerprint 返回生成期冻结的完整 Schema 指纹。\n"+
			"func (dispatcher *%s) Fingerprint() %s.ContractFingerprint { return %sFingerprint }\n\n",
		name,
		item.name,
		name,
		item.name,
		item.name,
		item.name,
		item.name,
		rpcAlias,
		name,
		name,
		rpcAlias,
		contractPrefix,
		name,
		rpcAlias,
		contractPrefix,
	)
	fmt.Fprintf(
		body,
		"// Dispatch 解码请求、调用业务实现并按调用类型决定是否编码响应。\n"+
			"func (dispatcher *%s) Dispatch(ctx %s.Context, methodID %s.MethodID, kind %s.CallKind, request []byte, response %s.ResponseWriter) (%s.ResponseWriter, error) {\n"+
			"\tswitch methodID {\n",
		name,
		contextAlias,
		rpcAlias,
		rpcAlias,
		rpcAlias,
		rpcAlias,
	)
	for _, candidate := range item.methods {
		prefix := upperFirst(contractPrefix + candidate.name)
		fmt.Fprintf(body, "\tcase %s%sMethodID:\n", contractPrefix, candidate.name)
		if len(candidate.inputs) == 0 {
			fmt.Fprintf(body, "\t\tif err := decode%sRequest(request); err != nil { return response, err }\n", prefix)
		} else {
			body.WriteString("\t\t")
			for index, input := range candidate.inputs {
				if index > 0 {
					body.WriteString(", ")
				}
				body.WriteString(input.name)
			}
			fmt.Fprintf(body, ", err := decode%sRequest(request)\n", prefix)
			body.WriteString("\t\tif err != nil { return response, err }\n")
		}

		body.WriteString("\t\tif kind == " + rpcAlias + ".CallNotify {\n")
		renderBusinessCall(
			body,
			"\t\t\t",
			"dispatcher.impl."+candidate.name,
			candidate,
			false,
			"",
		)
		body.WriteString("\t\t}\n")
		body.WriteString("\t\tif kind != " + rpcAlias + ".CallRequest {\n")
		body.WriteString("\t\t\treturn response, errs.ErrInvalidArgument\n\t\t}\n")
		if candidate.notifyOnly {
			body.WriteString("\t\treturn response, errs.ErrInvalidArgument\n")
		} else {
			renderBusinessCall(
				body,
				"\t\t",
				"dispatcher.impl."+candidate.name,
				candidate,
				true,
				"encode"+prefix+"Response",
			)
		}
	}
	body.WriteString("\tdefault:\n\t\treturn response, errs.ErrRPCMethodNotFound\n\t}\n}\n\n")
	imports.add("github.com/duanhf2012/origin/v3/errs", "errs")
}

// renderBusinessCall 按返回值分类生成业务调用、error 检查和可选响应编码。
func renderBusinessCall(
	body *bytes.Buffer,
	indent string,
	function string,
	candidate *method,
	respond bool,
	responseEncoder string,
) {
	if respond {
		if len(candidate.outputs) > 0 || candidate.hasError {
			body.WriteString(indent)
			first := true
			for _, output := range candidate.outputs {
				if !first {
					body.WriteString(", ")
				}
				body.WriteString(output.name)
				first = false
			}
			if candidate.hasError {
				if !first {
					body.WriteString(", ")
				}
				body.WriteString("callErr")
			}
			body.WriteString(" := ")
		} else {
			body.WriteString(indent)
		}
	} else {
		body.WriteString(indent)
		if candidate.hasError {
			for range candidate.outputs {
				body.WriteString("_, ")
			}
			body.WriteString("callErr := ")
		}
	}
	fmt.Fprintf(body, "%s(ctx", function)
	for _, input := range candidate.inputs {
		body.WriteString(", " + input.name)
	}
	body.WriteString(")\n")
	if candidate.hasError {
		body.WriteString(indent + "if callErr != nil { return response, callErr }\n")
	}
	if respond {
		fmt.Fprintf(body, "%sif err := %s(&response", indent, responseEncoder)
		for _, output := range candidate.outputs {
			body.WriteString(", " + output.name)
		}
		body.WriteString("); err != nil { return response, err }\n")
		body.WriteString(indent + "return response, nil\n")
	} else {
		body.WriteString(indent + "return response, nil\n")
	}
}

// parameterDeclarations 按契约位置顺序生成带前导逗号的参数声明。
func parameterDeclarations(imports *importSet, parameters []parameter) string {
	var builder strings.Builder
	for _, parameter := range parameters {
		fmt.Fprintf(
			&builder,
			", %s %s",
			parameter.name,
			imports.typeName(parameter.typ),
		)
	}
	return builder.String()
}

// parameterNames 按契约位置顺序生成带前导逗号的实参列表。
func parameterNames(parameters []parameter) string {
	var builder strings.Builder
	for _, parameter := range parameters {
		builder.WriteString(", " + parameter.name)
	}
	return builder.String()
}

// byteArrayLiteral 生成不依赖运行时解析的固定字节数组字面量。
func byteArrayLiteral(value []byte) string {
	var builder strings.Builder
	builder.WriteByte('{')
	for index, item := range value {
		if index > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, "0x%02x", item)
	}
	builder.WriteByte('}')
	return builder.String()
}

// lowerFirst 把导出契约名转换为当前包内稳定前缀。
func lowerFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}

// upperFirst 把内部方法前缀转换为生成函数使用的导出式首字母。
func upperFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
