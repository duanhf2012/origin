package config

func mergeNodes(destination, source *valueNode, path string) error {
	if destination.kind != source.kind {
		return duplicateNodeError(destination, source, path)
	}

	switch destination.kind {
	case kindMapping:
		indexByKey := make(map[string]int, len(destination.mapping))
		for index, entry := range destination.mapping {
			indexByKey[entry.key] = index
		}
		for _, sourceEntry := range source.mapping {
			childPath := joinPath(path, sourceEntry.key)
			index, exists := indexByKey[sourceEntry.key]
			if !exists {
				indexByKey[sourceEntry.key] = len(destination.mapping)
				destination.mapping = append(destination.mapping, sourceEntry)
				continue
			}
			if err := mergeNodes(destination.mapping[index].value, sourceEntry.value, childPath); err != nil {
				return err
			}
		}
		return nil
	case kindSequence:
		destination.sequence = append(destination.sequence, source.sequence...)
		return nil
	default:
		return duplicateNodeError(destination, source, path)
	}
}

func duplicateNodeError(first, second *valueNode, path string) error {
	if path == "" {
		path = "<root>"
	}
	return invalidConfigAt(
		second.source,
		"配置路径 %q 重复或类型冲突（首次定义 %s:%d:%d，原类型 %s，新类型 %s）",
		path,
		first.source.file,
		first.source.line,
		first.source.column,
		first.kindName(),
		second.kindName(),
	)
}
