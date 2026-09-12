package config

// 将主配置深层合并到默认值上
func mergeNodes(base, override *configNode) *configNode {
	if override == nil {
		return cloneNode(base)
	}
	if base == nil || override.kind == valueNull {
		return cloneNode(override)
	}

	if override.kind == valuePointer && base.kind == valuePointer {
		merged := cloneNode(override)
		merged.child = mergeNodes(base.child, override.child)
		return merged
	}
	if override.kind == valueObject && base.kind == valueObject {
		merged := cloneNode(base)
		for key, child := range override.object {
			merged.object[key] = mergeNodes(base.object[key], child)
		}
		return merged
	}

	return cloneNode(override)
}

// 配置节点深拷贝
func cloneNode(source *configNode) *configNode {
	if source == nil {
		return nil
	}
	cloned := &configNode{
		kind:   source.kind,
		schema: source.schema,
		scalar: source.scalar,
		base:   source.base,
	}
	if source.object != nil {
		cloned.object = make(map[string]*configNode, len(source.object))
		for key, child := range source.object {
			cloned.object[key] = cloneNode(child)
		}
	}
	if source.list != nil {
		cloned.list = make([]*configNode, len(source.list))
		for index, child := range source.list {
			cloned.list[index] = cloneNode(child)
		}
	}
	cloned.child = cloneNode(source.child)

	return cloned
}
