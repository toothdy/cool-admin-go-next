package codegen

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

func resolveDeps(nodes []graphComponent, providers []graphProvider, modules map[string]Module) ([]graphDependency, error) {
	var dependencies []graphDependency
	for _, consumer := range nodes {
		for parameterIndex, parameter := range consumer.constructor.types {
			position := parameterPosition(consumer.constructor, parameterIndex)
			matches, illegal := matchProviders(consumer, parameterIndex, parameter, providers, modules)
			if len(matches) == 0 {
				if len(illegal) > 0 {
					labels := providerLabels(illegal, providers)
					message := fmt.Sprintf("跨模块依赖 %s.%s -> %s 仅允许目标模块的具体 Provider 或 contract/** 接口，Config 和 Seed 不能跨模块注入", consumer.component.module, consumer.component.name, strings.Join(labels, ", "))
					return nil, graphError("CG035", message, position)
				}
				message := fmt.Sprintf("构造器 %s.%s 的参数 %s 缺少 Provider", consumer.component.module, consumer.component.name, types.TypeString(parameter, qualifier))
				return nil, graphError("CG032", message, position)
			}
			if len(matches) > 1 {
				labels := providerLabels(matches, providers)
				message := fmt.Sprintf("构造器 %s.%s 的参数 %s 存在歧义 Provider: %s", consumer.component.module, consumer.component.name, types.TypeString(parameter, qualifier), strings.Join(labels, ", "))
				return nil, graphError("CG033", message, position)
			}
			providerIndex := matches[0]
			provider := providers[providerIndex]
			dependencies = append(dependencies, graphDependency{
				consumer:          consumer.index,
				parameterIndex:    parameterIndex,
				position:          position,
				provider:          providerIndex,
				providerComponent: provider.componentIndex,
			})
		}
	}
	return dependencies, nil
}

func matchProviders(consumer graphComponent, parameterIndex int, parameter types.Type, providers []graphProvider, modules map[string]Module) ([]int, []int) {
	var matches, illegal []int
	targetModule := parameterModule(consumer.constructor, parameterIndex, modules)
	for index, provider := range providers {
		if provider.typ == nil || !types.AssignableTo(provider.typ, parameter) {
			continue
		}
		if provider.provider.kind == ProviderKindDescriptor && !types.Identical(types.Unalias(provider.typ), types.Unalias(parameter)) {
			continue
		}
		if provider.provider.kind == ProviderKindEnqueuer && isOutboxType(parameter, "Enqueuer") {
			matches = append(matches, index)
			continue
		}
		isLocalDependency := targetModule == "" || targetModule == consumer.component.module
		if isLocalDependency && provider.provider.module == consumer.component.module ||
			!isLocalDependency && provider.provider.module == targetModule && allowCrossModuleDep(consumer.constructor, parameterIndex, parameter, provider, modules) {
			matches = append(matches, index)
		} else if provider.provider.module == frameworkModuleKey {
			// 框架模块 .framework 的 Provider（如数据库 Runtime）允许跨模块注入
			matches = append(matches, index)
		} else {
			illegal = append(illegal, index)
		}
	}
	return matches, illegal
}

func parameterModule(constructor Constructor, parameterIndex int, modules map[string]Module) string {
	if parameterIndex >= len(constructor.paramDecls) {
		return ""
	}
	file := constructor.paramDecls[parameterIndex].File
	for key, current := range modules {
		if current.root != "" && strings.HasPrefix(file, current.root+"/") {
			return key
		}
	}
	return ""
}

func providerLabels(indexes []int, providers []graphProvider) []string {
	labels := make([]string, len(indexes))
	for index, providerIndex := range indexes {
		labels[index] = providerLabel(providers[providerIndex])
	}
	sort.Strings(labels)
	return labels
}

func allowCrossModuleDep(constructor Constructor, parameterIndex int, parameter types.Type, provider graphProvider, modules map[string]Module) bool {
	if provider.provider.kind == ProviderKindConfig || provider.provider.kind == ProviderKindSeed {
		return false
	}
	if _, ok := types.Unalias(parameter).Underlying().(*types.Interface); !ok {
		return true
	}
	if parameterIndex >= len(constructor.paramDecls) {
		return false
	}
	declaration := constructor.paramDecls[parameterIndex]
	module, exists := modules[provider.provider.module]
	if !exists || module.root == "" || declaration.File == "" {
		return false
	}
	return strings.HasPrefix(declaration.File, module.root+"/contract/")
}

func parameterPosition(constructor Constructor, index int) Position {
	if index < len(constructor.paramPos) && constructor.paramPos[index].File != "" {
		return constructor.paramPos[index]
	}
	return constructor.position
}

func exportDeps(dependencies []graphDependency, nodes []graphComponent, providers []graphProvider) []Dependency {
	result := make([]Dependency, len(dependencies))
	for index, dependency := range dependencies {
		result[index] = Dependency{
			consumer:       nodes[dependency.consumer].component,
			parameterIndex: dependency.parameterIndex,
			position:       dependency.position,
			provider:       providers[dependency.provider].provider,
		}
	}
	return result
}

func moduleDeps(dependencies []graphDependency, nodes []graphComponent, providers []graphProvider) []ModuleDependency {
	seen := make(map[string]bool)
	var result []ModuleDependency
	for _, dependency := range dependencies {
		consumer := nodes[dependency.consumer].component.module
		providerDefinition := providers[dependency.provider].provider
		if providerDefinition.kind == ProviderKindEnqueuer {
			continue
		}
		provider := providerDefinition.module
		if consumer == provider {
			continue
		}
		key := consumer + ":" + provider
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, ModuleDependency{consumer: consumer, position: dependency.position, provider: provider})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].consumer != result[right].consumer {
			return result[left].consumer < result[right].consumer
		}
		return result[left].provider < result[right].provider
	})
	return result
}
