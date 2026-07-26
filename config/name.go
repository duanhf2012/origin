package config

import (
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

type structField struct {
	name  string
	index []int
}

type structFields struct {
	byName map[string]structField
	err    error
}

func collectStructFields(target reflect.Type) structFields {
	result := structFields{
		byName: make(map[string]structField),
	}
	collectFields(target, target, nil, result.byName, make(map[reflect.Type]bool), &result.err)
	return result
}

func collectFields(
	root reflect.Type,
	target reflect.Type,
	parentIndex []int,
	fields map[string]structField,
	visiting map[reflect.Type]bool,
	resultErr *error,
) {
	if *resultErr != nil {
		return
	}
	if visiting[target] {
		*resultErr = invalidConfig("配置结构体 " + root.String() + " 存在递归匿名嵌入")
		return
	}
	visiting[target] = true
	defer delete(visiting, target)

	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		if field.PkgPath != "" {
			continue
		}

		tagName, ignored := jsonTagName(field.Tag.Get("json"))
		if ignored {
			continue
		}
		fieldIndex := appendIndex(parentIndex, index)

		embeddedType := field.Type
		if embeddedType.Kind() == reflect.Pointer {
			embeddedType = embeddedType.Elem()
		}
		if field.Anonymous && tagName == "" && embeddedType.Kind() == reflect.Struct {
			collectFields(root, embeddedType, fieldIndex, fields, visiting, resultErr)
			continue
		}

		name := tagName
		if name == "" {
			name = snakeCase(field.Name)
		}
		if previous, exists := fields[name]; exists {
			*resultErr = invalidConfig(
				"配置结构体 " + root.String() + " 的字段 " +
					field.Name + " 与索引 " + formatFieldIndex(previous.index) +
					" 映射为相同名称 " + name,
			)
			return
		}
		fields[name] = structField{name: name, index: fieldIndex}
	}
}

func appendIndex(parent []int, index int) []int {
	result := make([]int, len(parent)+1)
	copy(result, parent)
	result[len(parent)] = index
	return result
}

func jsonTagName(tag string) (name string, ignored bool) {
	if tag == "" {
		return "", false
	}
	name, _, _ = strings.Cut(tag, ",")
	if name == "-" {
		return "", true
	}
	return name, false
}

func snakeCase(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(name) + 4)
	for index, current := range runes {
		if index > 0 && unicode.IsUpper(current) {
			previous := runes[index-1]
			var next rune
			if index+1 < len(runes) {
				next = runes[index+1]
			}
			if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
				unicode.IsUpper(previous) && next != 0 && unicode.IsLower(next) {
				builder.WriteByte('_')
			}
		}
		builder.WriteRune(unicode.ToLower(current))
	}
	return builder.String()
}

func formatFieldIndex(index []int) string {
	var builder strings.Builder
	for position, value := range index {
		if position > 0 {
			builder.WriteByte('.')
		}
		builder.WriteString(strconv.Itoa(value))
	}
	return builder.String()
}
