package config

import (
	"encoding"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

type valueDecoder struct {
	fields map[reflect.Type]structFields
}

func (decoder *valueDecoder) decode(destination reflect.Value, node *valueNode) error {
	if !destination.CanSet() {
		return invalidConfigAt(node.source, "配置目标 %s 不可写", destination.Type())
	}

	if destination.Kind() == reflect.Pointer {
		if node.kind == kindNull {
			destination.SetZero()
			return nil
		}
		next := reflect.New(destination.Type().Elem())
		if !destination.IsNil() {
			next.Elem().Set(destination.Elem())
		}
		if err := decoder.decode(next.Elem(), node); err != nil {
			return err
		}
		destination.Set(next)
		return nil
	}

	if node.kind == kindNull {
		switch destination.Kind() {
		case reflect.Interface, reflect.Map, reflect.Slice:
			destination.SetZero()
			return nil
		default:
			return invalidConfigAt(node.source, "Null 不能解码到 %s", destination.Type())
		}
	}

	if destination.CanAddr() && destination.Addr().Type().Implements(textUnmarshalerType) {
		return decodeText(destination, node)
	}

	switch destination.Kind() {
	case reflect.Interface:
		value, err := nodeToAny(node)
		if err != nil {
			return err
		}
		if value == nil {
			destination.SetZero()
			return nil
		}
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
		if node.kind != kindString {
			return typeMismatch(node, destination.Type())
		}
		destination.SetString(node.scalar.(string))
		return nil
	case reflect.Bool:
		value, err := boolValue(node)
		if err != nil {
			return typeMismatch(node, destination.Type())
		}
		destination.SetBool(value)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value, err := intValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetInt(value)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		value, err := uintValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetUint(value)
		return nil
	case reflect.Float32, reflect.Float64:
		value, err := floatValue(node, destination.Type().Bits())
		if err != nil {
			return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
		}
		destination.SetFloat(value)
		return nil
	default:
		return invalidConfigAt(node.source, "不支持解码到 %s", destination.Type())
	}
}

func decodeText(destination reflect.Value, node *valueNode) error {
	if node.kind != kindString {
		return typeMismatch(node, destination.Type())
	}
	unmarshaler := destination.Addr().Interface().(encoding.TextUnmarshaler)
	if err := unmarshaler.UnmarshalText([]byte(node.scalar.(string))); err != nil {
		if node.envDerived {
			return invalidConfigAt(node.source, "环境变量值不能解码到 %s", destination.Type())
		}
		return invalidConfigAt(node.source, "配置值不能解码到 %s: %s", destination.Type(), err)
	}
	return nil
}

func (decoder *valueDecoder) decodeStruct(destination reflect.Value, node *valueNode) error {
	if node.kind != kindMapping {
		return typeMismatch(node, destination.Type())
	}
	fields := decoder.fieldsFor(destination.Type())
	if fields.err != nil {
		return fields.err
	}
	for _, entry := range node.mapping {
		field, exists := fields.byName[entry.key]
		if !exists {
			return invalidConfigAt(entry.keySource, "未知配置字段 %q（目标 %s）", entry.key, destination.Type())
		}
		fieldValue, err := writableField(destination, field.index)
		if err != nil {
			return invalidConfigAt(entry.keySource, "%s", err)
		}
		if err := decoder.decode(fieldValue, entry.value); err != nil {
			return err
		}
	}
	return nil
}

func (decoder *valueDecoder) fieldsFor(target reflect.Type) structFields {
	if fields, exists := decoder.fields[target]; exists {
		return fields
	}
	fields := collectStructFields(target)
	decoder.fields[target] = fields
	return fields
}

func writableField(root reflect.Value, index []int) (reflect.Value, error) {
	current := root
	for position, fieldIndex := range index {
		if current.Kind() == reflect.Pointer {
			next := reflect.New(current.Type().Elem())
			if !current.IsNil() {
				next.Elem().Set(current.Elem())
			}
			current.Set(next)
			current = next.Elem()
		}
		if current.Kind() != reflect.Struct || fieldIndex >= current.NumField() {
			return reflect.Value{}, fmt.Errorf("配置字段索引无效")
		}
		current = current.Field(fieldIndex)
		if position < len(index)-1 && current.Kind() == reflect.Pointer {
			next := reflect.New(current.Type().Elem())
			if !current.IsNil() {
				next.Elem().Set(current.Elem())
			}
			current.Set(next)
			current = next.Elem()
		}
	}
	if !current.CanSet() {
		return reflect.Value{}, fmt.Errorf("配置字段不可写")
	}
	return current, nil
}

func (decoder *valueDecoder) decodeMap(destination reflect.Value, node *valueNode) error {
	if node.kind != kindMapping {
		return typeMismatch(node, destination.Type())
	}
	result := reflect.MakeMapWithSize(destination.Type(), destination.Len()+len(node.mapping))
	if !destination.IsNil() {
		iterator := destination.MapRange()
		for iterator.Next() {
			result.SetMapIndex(iterator.Key(), iterator.Value())
		}
	}

	for _, entry := range node.mapping {
		key, err := decodeMapKey(entry.key, destination.Type().Key())
		if err != nil {
			return invalidConfigAt(entry.keySource, "Map Key %q 不能解码到 %s", entry.key, destination.Type().Key())
		}
		value := reflect.New(destination.Type().Elem()).Elem()
		if previous := result.MapIndex(key); previous.IsValid() {
			value.Set(previous)
		}
		if err := decoder.decode(value, entry.value); err != nil {
			return err
		}
		result.SetMapIndex(key, value)
	}
	destination.Set(result)
	return nil
}

func decodeMapKey(key string, target reflect.Type) (reflect.Value, error) {
	value := reflect.New(target).Elem()
	if value.CanAddr() && value.Addr().Type().Implements(textUnmarshalerType) {
		unmarshaler := value.Addr().Interface().(encoding.TextUnmarshaler)
		if err := unmarshaler.UnmarshalText([]byte(key)); err != nil {
			return reflect.Value{}, err
		}
		return value, nil
	}
	switch target.Kind() {
	case reflect.String:
		value.SetString(key)
	case reflect.Bool:
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
		return reflect.Value{}, fmt.Errorf("unsupported map key")
	}
	return value, nil
}

func (decoder *valueDecoder) decodeSlice(destination reflect.Value, node *valueNode) error {
	if node.kind != kindSequence {
		return typeMismatch(node, destination.Type())
	}
	result := reflect.MakeSlice(destination.Type(), len(node.sequence), len(node.sequence))
	for index, child := range node.sequence {
		if err := decoder.decode(result.Index(index), child); err != nil {
			return err
		}
	}
	destination.Set(result)
	return nil
}

func (decoder *valueDecoder) decodeArray(destination reflect.Value, node *valueNode) error {
	if node.kind != kindSequence {
		return typeMismatch(node, destination.Type())
	}
	if len(node.sequence) != destination.Len() {
		return invalidConfigAt(
			node.source,
			"Sequence 长度 %d 不能解码到 %s",
			len(node.sequence),
			destination.Type(),
		)
	}
	for index, child := range node.sequence {
		if err := decoder.decode(destination.Index(index), child); err != nil {
			return err
		}
	}
	return nil
}

func nodeToAny(node *valueNode) (any, error) {
	switch node.kind {
	case kindNull:
		return nil, nil
	case kindBool, kindInteger, kindUnsigned, kindFloat, kindString:
		return node.scalar, nil
	case kindSequence:
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
		return nil, invalidConfigAt(node.source, "配置节点类型无效")
	}
}

func boolValue(node *valueNode) (bool, error) {
	if node.kind == kindBool {
		return node.scalar.(bool), nil
	}
	if node.kind == kindString && node.envDerived {
		switch node.scalar.(string) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
	}
	return false, fmt.Errorf("expected bool")
}

func intValue(node *valueNode, bits int) (int64, error) {
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
	if bits < 64 {
		minimum := -(int64(1) << (bits - 1))
		maximum := (int64(1) << (bits - 1)) - 1
		if value < minimum || value > maximum {
			return 0, fmt.Errorf("整数溢出")
		}
	}
	return value, nil
}

func uintValue(node *valueNode, bits int) (uint64, error) {
	var value uint64
	switch node.kind {
	case kindUnsigned:
		value = node.scalar.(uint64)
	case kindInteger:
		signed := node.scalar.(int64)
		if signed < 0 {
			return 0, fmt.Errorf("负数不能解码到无符号整数")
		}
		value = uint64(signed)
	case kindString:
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
	if bits < 64 && value >= uint64(1)<<bits {
		return 0, fmt.Errorf("无符号整数溢出")
	}
	return value, nil
}

func floatValue(node *valueNode, bits int) (float64, error) {
	var value float64
	switch node.kind {
	case kindFloat:
		value = node.scalar.(float64)
	case kindInteger:
		value = float64(node.scalar.(int64))
	case kindUnsigned:
		value = float64(node.scalar.(uint64))
	case kindString:
		if !node.envDerived {
			return 0, fmt.Errorf("类型不匹配")
		}
		parsed, err := strconv.ParseFloat(node.scalar.(string), bits)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return 0, fmt.Errorf("环境变量不是有效浮点数")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("类型不匹配")
	}
	if bits == 32 && (value > math.MaxFloat32 || value < -math.MaxFloat32) {
		return 0, fmt.Errorf("浮点数溢出")
	}
	return value, nil
}

func typeMismatch(node *valueNode, target reflect.Type) error {
	return invalidConfigAt(
		node.source,
		"配置节点 %s 不能解码到 %s",
		node.kindName(),
		target,
	)
}
