package module

import (
	"fmt"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
)

func compileOutbox(
	definition OutboxDefinition,
	components []Component,
	componentIndexes map[componentKey]int,
	providers []Provider,
	dependencies []Dependency,
	lifecycles []Lifecycle,
) (OutboxGraph, error) {
	producers, err := compileRole("Producer", definition.Producers, components, componentIndexes, 0)
	if err != nil {
		return OutboxGraph{}, err
	}
	consumers, err := compileRole("Consumer Definition", definition.Consumers, components, componentIndexes, 0)
	if err != nil {
		return OutboxGraph{}, err
	}
	publishers, err := compileRole("Publisher", definition.Publishers, components, componentIndexes, 1)
	if err != nil {
		return OutboxGraph{}, err
	}
	adapters, err := compileRole("Consumer Adapter", definition.ConsumerAdapters, components, componentIndexes, 1)
	if err != nil {
		return OutboxGraph{}, err
	}
	workers, err := compileRole("Worker", definition.Workers, components, componentIndexes, 1)
	if err != nil {
		return OutboxGraph{}, err
	}
	runtimes, err := compileRole("Consumer Runtime", definition.ConsumerRuntimes, components, componentIndexes, 1)
	if err != nil {
		return OutboxGraph{}, err
	}
	graph := OutboxGraph{producers: producers, consumers: consumers}
	graph.publisher = firstComponent(publishers)
	graph.consumerAdapter = firstComponent(adapters)
	graph.worker = firstComponent(workers)
	graph.consumerRuntime = firstComponent(runtimes)
	if err = checkProducer(graph, providers, dependencies, lifecycles); err != nil {
		return OutboxGraph{}, err
	}
	if err = checkConsumer(graph, providers, dependencies, lifecycles); err != nil {
		return OutboxGraph{}, err
	}

	return graph, nil
}

func compileRole(
	label string,
	definitions []ComponentDefinition,
	components []Component,
	indexes map[componentKey]int,
	maximum int,
) ([]Component, error) {
	if maximum > 0 && len(definitions) > maximum {
		return nil, exception.Core(fmt.Sprintf("Outbox %s 只能存在一个", label))
	}
	result := make([]Component, len(definitions))
	seen := make(map[componentKey]bool, len(definitions))
	for index, definition := range definitions {
		key := componentDefKey(definition)
		componentIndex, exists := indexes[key]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("Outbox %s 组件不存在: %s", label, componentKeyLabel(key)))
		}
		if seen[key] {
			return nil, exception.Core(fmt.Sprintf("Outbox %s 组件重复: %s", label, componentKeyLabel(key)))
		}
		seen[key] = true
		result[index] = components[componentIndex]
	}
	return result, nil
}

func checkProducer(
	graph OutboxGraph,
	providers []Provider,
	dependencies []Dependency,
	lifecycles []Lifecycle,
) error {
	if len(graph.producers) == 0 {
		if graph.worker != nil {
			return exception.Core("无 Outbox Producer 时不能注册 Worker")
		}
		return nil
	}
	if graph.publisher == nil || graph.worker == nil {
		return exception.Core("Outbox Producer 需要唯一 Publisher 和 Worker")
	}
	if countProviderKind(providers, ProviderKindEnqueuer) != 1 {
		return exception.Core("Outbox Producer 需要唯一 Enqueuer Provider")
	}
	for _, producer := range graph.producers {
		if !hasDependencyKind(dependencies, producer, ProviderKindEnqueuer) {
			return exception.Core("Outbox Producer 必须直接依赖 Enqueuer: " + componentLabel(producer))
		}
	}
	if !hasDependency(dependencies, *graph.worker, *graph.publisher) {
		return exception.Core("Outbox Worker 必须依赖 Publisher")
	}
	if !hasLifecycle(lifecycles, *graph.worker, false, true, true) {
		return exception.Core("Outbox Worker 必须实现启动和停止生命周期")
	}
	return nil
}

func checkConsumer(
	graph OutboxGraph,
	providers []Provider,
	dependencies []Dependency,
	lifecycles []Lifecycle,
) error {
	if len(graph.consumers) == 0 {
		if graph.consumerRuntime != nil {
			return exception.Core("无可靠 Consumer 时不能注册 Consumer Runtime")
		}
		return nil
	}
	if graph.consumerAdapter == nil || graph.consumerRuntime == nil {
		return exception.Core("可靠 Consumer 需要唯一 Consumer Adapter 和 Consumer Runtime")
	}
	for _, consumer := range graph.consumers {
		if !hasProvider(providers, consumer, ProviderKindConsumerDefinition) {
			return exception.Core("可靠 Consumer 缺少 Consumer Definition Provider: " + componentLabel(consumer))
		}
		if !hasDependency(dependencies, *graph.consumerRuntime, consumer) {
			return exception.Core("Consumer Runtime 必须依赖全部 Consumer Definition")
		}
	}
	if !hasDependency(dependencies, *graph.consumerRuntime, *graph.consumerAdapter) {
		return exception.Core("Consumer Runtime 必须依赖 Consumer Adapter")
	}
	if !hasLifecycle(lifecycles, *graph.consumerRuntime, true, true, true) {
		return exception.Core("Consumer Runtime 必须实现初始化、启动和停止生命周期")
	}
	return nil
}

func firstComponent(components []Component) *Component {
	if len(components) == 0 {
		return nil
	}
	component := components[0]
	return &component
}

func countProviderKind(providers []Provider, kind ProviderKind) int {
	count := 0
	for _, provider := range providers {
		if provider.kind == kind {
			count++
		}
	}
	return count
}

func hasDependencyKind(dependencies []Dependency, consumer Component, kind ProviderKind) bool {
	for _, dependency := range dependencies {
		if dependency.consumer == consumer && dependency.provider.kind == kind {
			return true
		}
	}
	return false
}

func hasDependency(dependencies []Dependency, consumer, provider Component) bool {
	for _, dependency := range dependencies {
		if dependency.consumer != consumer || !isComponentProvider(dependency.provider.kind) {
			continue
		}
		candidate := Component{
			module:      dependency.provider.module,
			packagePath: dependency.provider.packagePath,
			name:        dependency.provider.name,
		}
		if candidate == provider {
			return true
		}
	}
	return false
}

func hasProvider(providers []Provider, component Component, kind ProviderKind) bool {
	for _, provider := range providers {
		if provider.kind == kind && provider.module == component.module &&
			provider.packagePath == component.packagePath && provider.name == component.name {
			return true
		}
	}
	return false
}

func hasLifecycle(
	lifecycles []Lifecycle,
	component Component,
	initializer bool,
	starter bool,
	stopper bool,
) bool {
	for _, lifecycle := range lifecycles {
		if lifecycle.component == component {
			return lifecycle.initializer == initializer && lifecycle.starter == starter &&
				lifecycle.stopper == stopper && lifecycle.supervisor
		}
	}
	return false
}

func componentLabel(component Component) string {
	return component.packagePath + "." + component.name
}
