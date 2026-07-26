package config

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

// textUnmarshalerType 是不可变反射元数据，用于识别自定义文本配置类型。
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// valueDecoder 保存一次 LoadDir 调用内的结构体字段模型缓存。
type valueDecoder struct {
	// fields 只缓存本次加载中实际遇到的结构体类型。
	fields map[reflect.Type]structFields
}

// decode 把单个内部节点严格写入可设置的目标值。
func (decoder *valueDecoder) decode(destination reflect.Value, node *valueNode) error {
	// 所有递归入口都验证可写性，避免 reflect.Set 在错误配置下 panic。
	if !destination.CanSet() {
		return invalidConfigAt(node.source, "配置目标 %s 不可写", destination.Type())
	}

	// Pointer 每次更新都创建新对象，以免失败路径污染调用方默认指针。
	if destination.Kind() == reflect.Pointer {
		if node.kind == kindNull {
			// Null 明确清空可空指针。
			destination.SetZero()
			return nil
		}
		// 新对象先复制原元素，保留配置未出现字段的默认值。
		next := reflect.New(destination.Type().Elem())
		if !destination.IsNil() {
			next.Elem().Set(destination.Elem())
		}
		// 递归成功后才把新指针提交到当前目标。
		if err := decoder.decode(next.Elem(), node); err != nil {
			return err
		}
		destination.Set(next)
		return nil
	}

	// 非指针 Null 只允许写入 Go 中自然可空的接口、Map 和 Slice。
	if node.kind == kindNull {
		switch destination.Kind() {
		case reflect.Interface, reflect.Map, reflect.Slice:
			destination.SetZero()
			return nil
		default:
			return invalidConfigAt(node.source, "Null 不能解码到 %s", destination.Type())
		}
	}

	// 自定义文本类型优先于 Kind 分支，例如 Duration 和 ByteSize 底层都是整数。
	if destination.CanAddr() && destination.Addr().Type().Implements(textUnmarshalerType) {
		return decodeText(destination, node)
	}

	// 其余目标按 Go Kind 使用严格转换，不执行 YAML 弱类型规则。
	switch destination.Kind() {
	case reflect.Interface:
		// 空接口恢复为普通 Go 值树；非空接口还需要赋值兼容检查。
		value, err := nodeToAny(node)
		if err != nil {
			return err
		}
		if value == nil {
			destination.SetZero()
			return nil
		}
		// reflect.Set 前验证动态值是否实现目标接口，避免 panic。
		reflected := reflect.ValueOf(value)
		if !reflected.Type().AssignableTo(destination.Type()) {
			return invalidConfigAt(node.source, "配置值不能解码到 %s", destination.Type())
		}
		destination.Set(reflected)
		return nil
	case reflect.Struct:
		return decoder.decodeStruct(destination, node)
	case reflect.Map:
		return decoder.decodeMap(destination, node)
	case reflect.Slice:
		return decoder.decodeSlice(destination, node)
	case reflect.Array:
		return decoder.decodeArray(destination, node)
	case reflect.String:
		// 普通配置数值不能自动转字符串。
		if node.kind != kindString {
			return typeMismatch(node, destination.Type())
		}
		destination.SetString(node.scalar.(string))
		return nil
	case reflect.Bool:
		// Bool 仅接受原生 Bool 或完整环境变量得到的严格 true/false。
		value, err := boolValue(node)
		if err != nil {
			return typeMismatch(node, destination.Type())
		}
		destination.SetBool(value)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// intValue 同时处理符号和目标位宽溢出。
		value, err := intValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetInt(value)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		// uintValue 拒绝负数并检查目标位宽。
		value, err := uintValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetUint(value)
		return nil
	case reflect.Float32, reflect.Float64:
		// floatValue 允许数字节点，并拒绝环境变量 NaN/Inf。
		value, err := floatValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetFloat(value)
		return nil
	default:
		// Channel、Func、Complex 等类型没有配置语义。
		return invalidConfigAt(node.source, "不支持解码到 %s", destination.Type())
	}
}

// decodeText 调用目标类型的 encoding.TextUnmarshaler。
func decodeText(destination reflect.Value, node *valueNode) error {
	// 文本解码契约只接受字符串 Scalar。
	if node.kind != kindString {
		return typeMismatch(node, destination.Type())
	}
	// destination 已确认可寻址且实现接口，可以安全断言。
	unmarshaler := destination.Addr().Interface().(encoding.TextUnmarshaler)
	if err := unmarshaler.UnmarshalText([]byte(node.scalar.(string))); err != nil {
		// 环境来源错误不得附带第三方解析错误，防止其中回显秘密值。
		if node.envDerived {
			return invalidConfigAt(node.source, "环境变量值不能解码到 %s", destination.Type())
		}
		// 普通配置值可以保留解析原因，帮助定位单位或格式问题。
		return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
	}
	return nil
}

// decodeStruct 按预计算字段模型严格解码一个 Mapping。
func (decoder *valueDecoder) decodeStruct(destination reflect.Value, node *valueNode) error {
	// 结构体只接受 Mapping，拒绝 Scalar 和 Sequence。
	if node.kind != kindMapping {
		return typeMismatch(node, destination.Type())
	}
	// 字段模型在本次 LoadDir 中按类型缓存，冲突在读取任何值前返回。
	fields := decoder.fieldsFor(destination.Type())
	if fields.err != nil {
		return fields.err
	}
	// 按内部树稳定顺序逐字段解码。
	for _, entry := range node.mapping {
		field, exists := fields.byName[entry.key]
		if !exists {
			// 未知字段错误定位到 Key 本身。
			return invalidConfigAt(entry.keySource, "未知配置字段 %q（目标 %s）", entry.key, destination.Type())
		}
		// 匿名指针路径需要按需复制并建立可写字段。
		fieldValue, err := writableField(destination, field.index)
		if err != nil {
			return invalidConfigAt(entry.keySource, "%s", err)
		}
		// 子节点解码失败直接返回，外层临时目标保证整体原子性。
		if err := decoder.decode(fieldValue, entry.value); err != nil {
			return err
		}
	}
	return nil
}

// fieldsFor 返回一次加载调用内缓存的结构体字段模型。
func (decoder *valueDecoder) fieldsFor(target reflect.Type) structFields {
	// 命中缓存时不重复遍历反射字段。
	if fields, exists := decoder.fields[target]; exists {
		return fields
	}
	// 首次使用时收集并连同可能的模型错误一起缓存。
	fields := collectStructFields(target)
	decoder.fields[target] = fields
	return fields
}

// writableField 沿反射索引路径取得字段，并复制途经的匿名指针。
func writableField(root reflect.Value, index []int) (reflect.Value, error) {
	// 从根结构体开始逐段向下解析索引。
	current := root
	for position, fieldIndex := range index {
		// 当前层是指针时建立新对象，避免修改调用方默认对象。
		if current.Kind() == reflect.Pointer {
			next := reflect.New(current.Type().Elem())
			if !current.IsNil() {
				next.Elem().Set(current.Elem())
			}
			current.Set(next)
			current = next.Elem()
		}
		// 每段都验证结构体种类和索引范围，防止模型损坏导致 panic。
		if current.Kind() != reflect.Struct || fieldIndex >= current.NumField() {
			return reflect.Value{}, fmt.Errorf("配置字段索引无效")
		}
		// 进入当前字段。
		current = current.Field(fieldIndex)
		// 非末级匿名指针同样复制或初始化，下一轮才能访问其结构体字段。
		if position < len(index)-1 && current.Kind() == reflect.Pointer {
			next := reflect.New(current.Type().Elem())
			if !current.IsNil() {
				next.Elem().Set(current.Elem())
			}
			current.Set(next)
			current = next.Elem()
		}
	}
	// 最终字段必须可设置；未导出字段理论上已在模型收集阶段排除。
	if !current.CanSet() {
		return reflect.Value{}, fmt.Errorf("配置字段不可写")
	}
	// 返回字段本身，由 decode 按其目标类型继续处理。
	return current, nil
}

// decodeMap 把 Mapping 解码到开放动态 Key 的强类型 Go Map。
func (decoder *valueDecoder) decodeMap(destination reflect.Value, node *valueNode) error {
	// Map 目标只接受 Mapping。
	if node.kind != kindMapping {
		return typeMismatch(node, destination.Type())
	}
	// 新建结果并预留默认 Map 与配置条目的总容量。
	result := reflect.MakeMapWithSize(destination.Type(), destination.Len()+len(node.mapping))
	if !destination.IsNil() {
		// 先复制默认条目，避免失败时或更新时修改原 Map。
		iterator := destination.MapRange()
		for iterator.Next() {
			result.SetMapIndex(iterator.Key(), iterator.Value())
		}
	}

	// 按配置条目逐个转换 Key 和 Value。
	for _, entry := range node.mapping {
		key, err := decodeMapKey(entry.key, destination.Type().Key())
		if err != nil {
			// Key 转换错误定位在 Key 位置。
			return invalidConfigAt(entry.keySource, "Map Key %q 不能解码到 %s", entry.key, destination.Type().Key())
		}
		// 为 Map Value 建立可设置临时值；已有条目先复制以保留默认字段。
		value := reflect.New(destination.Type().Elem()).Elem()
		if previous := result.MapIndex(key); previous.IsValid() {
			value.Set(previous)
		}
		// 子值全部解码成功后再写入结果 Map。
		if err := decoder.decode(value, entry.value); err != nil {
			return err
		}
		result.SetMapIndex(key, value)
	}
	// 完整 Mapping 成功后一次性替换目标 Map。
	destination.Set(result)
	return nil
}

// decodeMapKey 把字符串配置 Key 严格转换为 Go Map Key 类型。
func decodeMapKey(key string, target reflect.Type) (reflect.Value, error) {
	// 创建可设置的零值 Key。
	value := reflect.New(target).Elem()
	// 自定义文本 Key 优先处理，允许业务定义受控枚举类型。
	if value.CanAddr() && value.Addr().Type().Implements(textUnmarshalerType) {
		unmarshaler := value.Addr().Interface().(encoding.TextUnmarshaler)
		if err := unmarshaler.UnmarshalText([]byte(key)); err != nil {
			return reflect.Value{}, err
		}
		return value, nil
	}
	// 常用基础可比较类型使用标准库严格十进制转换。
	switch target.Kind() {
	case reflect.String:
		value.SetString(key)
	case reflect.Bool:
		// Bool Key 只接受完整小写 true/false。
		if key != "true" && key != "false" {
			return reflect.Value{}, fmt.Errorf("invalid bool")
		}
		value.SetBool(key == "true")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		parsed, err := strconv.ParseInt(key, 10, target.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		parsed, err := strconv.ParseUint(key, 10, target.Bits())
		if err != nil {
			return reflect.Value{}, err
		}
		value.SetUint(parsed)
	default:
		// 浮点、结构体和接口 Key 首版没有稳定文本规范。
		return reflect.Value{}, fmt.Errorf("unsupported map key")
	}
	// 返回已经设置且可作为 MapIndex 的目标类型值。
	return value, nil
}

// decodeSlice 把 Sequence 解码为新切片。
func (decoder *valueDecoder) decodeSlice(destination reflect.Value, node *valueNode) error {
	// Slice 只接受 Sequence。
	if node.kind != kindSequence {
		return typeMismatch(node, destination.Type())
	}
	// 配置出现 Slice 时替换默认 Slice，创建准确长度的新所有权。
	result := reflect.MakeSlice(destination.Type(), len(node.sequence), len(node.sequence))
	// 每个元素在新切片中就地解码。
	for index, child := range node.sequence {
		if err := decoder.decode(result.Index(index), child); err != nil {
			return err
		}
	}
	// 所有元素成功后提交，失败时目标保持原值。
	destination.Set(result)
	return nil
}

// decodeArray 把长度完全一致的 Sequence 解码到固定数组。
func (decoder *valueDecoder) decodeArray(destination reflect.Value, node *valueNode) error {
	// Array 只接受 Sequence。
	if node.kind != kindSequence {
		return typeMismatch(node, destination.Type())
	}
	// 固定数组无法截断或扩展，长度必须精确匹配。
	if len(node.sequence) != destination.Len() {
		return invalidConfigAt(
			node.source,
			"Sequence 长度 %d 不能解码到 %s",
			len(node.sequence),
			destination.Type(),
		)
	}
	// 逐元素写入外层临时目标。
	for index, child := range node.sequence {
		if err := decoder.decode(destination.Index(index), child); err != nil {
			return err
		}
	}
	return nil
}

// nodeToAny 把内部节点恢复为不携带来源元数据的普通 Go 值。
func nodeToAny(node *valueNode) (any, error) {
	// Scalar 可直接返回内部规范化值。
	switch node.kind {
	case kindNull:
		return nil, nil
	case kindBool, kindInteger, kindUnsigned, kindFloat, kindString:
		return node.scalar, nil
	case kindSequence:
		// Sequence 创建独立 []any，并递归转换每个元素。
		result := make([]any, len(node.sequence))
		for index, child := range node.sequence {
			value, err := nodeToAny(child)
			if err != nil {
				return nil, err
			}
			result[index] = value
		}
		return result, nil
	case kindMapping:
		// Mapping 创建独立 map[string]any，不暴露内部条目切片。
		result := make(map[string]any, len(node.mapping))
		for _, entry := range node.mapping {
			value, err := nodeToAny(entry.value)
			if err != nil {
				return nil, err
			}
			result[entry.key] = value
		}
		return result, nil
	default:
		// kindInvalid 表示内部不变量损坏。
		return nil, invalidConfigAt(node.source, "配置节点类型无效")
	}
}

// boolValue 读取 Bool 节点或完整环境变量得到的严格布尔文本。
func boolValue(node *valueNode) (bool, error) {
	// 原生 JSON/YAML Bool 直接返回。
	if node.kind == kindBool {
		return node.scalar.(bool), nil
	}
	// 只有 envDerived String 才允许跨 Scalar 类型转换。
	if node.kind == kindString && node.envDerived {
		switch node.scalar.(string) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	// 其他输入统一返回类型错误，不接受 yes/1 等宽松别名。
	return false, fmt.Errorf("expected bool")
}

// intValue 把节点转换到指定有符号位宽并检查范围。
func intValue(node *valueNode, bits int) (int64, error) {
	// 先规范为 int64；无符号值需额外检查 MaxInt64。
	var value int64
	switch node.kind {
	case kindInteger:
		value = node.scalar.(int64)
	case kindUnsigned:
		unsigned := node.scalar.(uint64)
		if unsigned > math.MaxInt64 {
			return 0, fmt.Errorf("整数溢出")
		}
		value = int64(unsigned)
	case kindString:
		// 普通字符串不是整数，完整环境变量才允许 ParseInt。
		if !node.envDerived {
			return 0, fmt.Errorf("类型不匹配")
		}
		parsed, err := strconv.ParseInt(node.scalar.(string), 10, bits)
		if err != nil {
			return 0, fmt.Errorf("环境变量不是有效整数")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("类型不匹配")
	}
	// 64 位以下目标需要显式计算最小值和最大值。
	if bits < 64 {
		minimum := -(int64(1) << (bits - 1))
		maximum := (int64(1) << (bits - 1)) - 1
		if value < minimum || value > maximum {
			return 0, fmt.Errorf("整数溢出")
		}
	}
	// value 已满足目标位宽。
	return value, nil
}

// uintValue 把节点转换到指定无符号位宽并检查负数和溢出。
func uintValue(node *valueNode, bits int) (uint64, error) {
	// 先规范为 uint64。
	var value uint64
	switch node.kind {
	case kindUnsigned:
		value = node.scalar.(uint64)
	case kindInteger:
		// 有符号负值不能转换为无符号配置。
		signed := node.scalar.(int64)
		if signed < 0 {
			return 0, fmt.Errorf("负数不能解码到无符号整数")
		}
		value = uint64(signed)
	case kindString:
		// 只有完整环境变量允许从字符串转换。
		if !node.envDerived {
			return 0, fmt.Errorf("类型不匹配")
		}
		parsed, err := strconv.ParseUint(node.scalar.(string), 10, bits)
		if err != nil {
			return 0, fmt.Errorf("环境变量不是有效无符号整数")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("类型不匹配")
	}
	// 64 位以下目标用 2^bits 作为第一个非法值。
	if bits < 64 && value >= uint64(1)<<bits {
		return 0, fmt.Errorf("无符号整数溢出")
	}
	// value 已满足目标位宽。
	return value, nil
}

// floatValue 把数字节点或完整环境变量转换到指定浮点位宽。
func floatValue(node *valueNode, bits int) (float64, error) {
	// JSON/YAML 整数允许无损或按 Go 规则转为浮点。
	var value float64
	switch node.kind {
	case kindFloat:
		value = node.scalar.(float64)
	case kindInteger:
		value = float64(node.scalar.(int64))
	case kindUnsigned:
		value = float64(node.scalar.(uint64))
	case kindString:
		// 只有完整环境变量允许从字符串解析浮点。
		if !node.envDerived {
			return 0, fmt.Errorf("类型不匹配")
		}
		parsed, err := strconv.ParseFloat(node.scalar.(string), bits)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			// 配置禁止 NaN 和 Inf，避免比较及序列化语义异常。
			return 0, fmt.Errorf("环境变量不是有效浮点数")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("类型不匹配")
	}
	// float32 目标额外检查有限值范围。
	if bits == 32 && (value > math.MaxFloat32 || value < -math.MaxFloat32) {
		return 0, fmt.Errorf("浮点数溢出")
	}
	// reflect.SetFloat 会按目标位宽完成最终表示。
	return value, nil
}

// typeMismatch 生成统一且带节点来源的严格类型错误。
func typeMismatch(node *valueNode, target reflect.Type) error {
	// 消息只包含节点种类和目标 Go 类型，不回显可能敏感的配置值。
	return invalidConfigAt(
		node.source,
		"配置节点 %s 不能解码到 %s",
		node.kindName(),
		target,
	)
}
