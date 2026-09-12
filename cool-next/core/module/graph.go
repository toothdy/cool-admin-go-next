package module

import (
	"fmt"
	"go/token"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/exception"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/route"
)

var tablePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// 已验证的静态模块图
type Graph struct {
	components   []Component
	dependencies []Dependency
	descriptors  []Descriptor
	lifecycles   []Lifecycle
	modules      []StaticModule
	outbox       OutboxGraph
	providers    []Provider
	routes       route.Table
	transports   []Component
	validated    bool
}

// 静态模块图构建输入
type GraphInput struct {
	Components   []ComponentDefinition        // 已排序的组件定义
	Controllers  []route.ControllerDefinition // Controller 静态定义
	Dependencies []DependencyDefinition       // 组件依赖定义
	Descriptors  []DescriptorDefinition       // 实体 Descriptor 定义
	Lifecycles   []LifecycleDefinition        // 组件生命周期定义
	Modules      []ModuleDefinition           // 模块静态定义
	Outbox       OutboxDefinition             // 可靠消息装配定义
	Providers    []ProviderDefinition         // Provider 定义
	Routes       []route.Definition           // 路由静态定义
	Transports   []ComponentDefinition        // Transport 组件引用
}

// 模块静态定义
type ModuleDefinition struct {
	Description       string         // 模块说明
	GlobalMiddlewares []ComponentRef // 全局中间件引用
	Key               string         // 模块身份键
	Middlewares       []ComponentRef // 模块中间件引用
	Name              string         // 展示名称
	Order             int            // 模块排序值
}

// Provider 类别
type ProviderKind string

const (
	ProviderKindConfig             ProviderKind = "config"          // 模块配置
	ProviderKindSeed               ProviderKind = "seed"            // 模块种子数据
	ProviderKindComponent          ProviderKind = "component"       // 普通组件
	ProviderKindDescriptor         ProviderKind = "descriptor"      // 实体 Descriptor
	ProviderKindBase               ProviderKind = "base"            // 实体 Base Service
	ProviderKindEnqueuer           ProviderKind = "outbox-enqueuer" // Outbox Enqueuer
	ProviderKindConsumerDefinition ProviderKind = "outbox-consumer" // Consumer Definition
)

// Provider 静态定义
type ProviderDefinition struct {
	Kind        ProviderKind // Provider 类别
	Module      string       // 所属模块身份键
	PackagePath string       // 声明包路径
	Name        string       // Provider 名称
	Type        string       // Go 类型文本
}

// 已排序的组件定义
type ComponentDefinition struct {
	Module      string // 所属模块身份键
	PackagePath string // 构造器包路径
	Name        string // 构造器名称
}

// 组件依赖定义
type DependencyDefinition struct {
	Consumer       ComponentDefinition // 依赖方组件
	ParameterIndex int                 // 构造器参数序号
	Provider       ProviderDefinition  // 被依赖 Provider
}

// 实体 Descriptor 定义
type DescriptorDefinition struct {
	Module   string             // 所属模块身份键
	Provider ProviderDefinition // Descriptor Provider
	Table    string             // 数据库表名
}

// 可靠消息角色定义
type OutboxDefinition struct {
	Producers        []ComponentDefinition // 直接依赖 Enqueuer 的组件
	Consumers        []ComponentDefinition // Consumer Definition 组件
	Publishers       []ComponentDefinition // Publisher 组件
	ConsumerAdapters []ComponentDefinition // Consumer Adapter 组件
	Workers          []ComponentDefinition // Worker 生命周期组件
	ConsumerRuntimes []ComponentDefinition // Consumer 生命周期组件
}

// 已验证的可靠消息角色图
type OutboxGraph struct {
	producers       []Component
	consumers       []Component
	publisher       *Component
	consumerAdapter *Component
	worker          *Component
	consumerRuntime *Component
}

// 只读模块静态描述
type StaticModule struct {
	description       string
	globalMiddlewares []ComponentRef
	identity          Identity
	middlewares       []ComponentRef
	name              string
	order             int
}

// 已登记的 Provider
type Provider struct {
	kind        ProviderKind
	module      string
	packagePath string
	name        string
	typ         string
}

// 已登记的组件
type Component struct {
	module      string
	packagePath string
	name        string
}

// 已登记的组件依赖
type Dependency struct {
	consumer       Component
	parameterIndex int
	provider       Provider
}

// 已登记的实体 Descriptor
type Descriptor struct {
	module   string
	provider Provider
	table    string
}

// 已验证的组件生命周期
type Lifecycle struct {
	component   Component
	initializer bool
	provider    Provider
	starter     bool
	stopper     bool
	supervisor  bool
}

type componentKey struct {
	module      string
	packagePath string
	name        string
}

type providerKey struct {
	kind        ProviderKind
	module      string
	packagePath string
	name        string
}

// 构建不可变静态模块图
func BuildGraph(input GraphInput) (Graph, error) {
	modules, moduleKeys, err := compileModules(input.Modules)
	if err != nil {
		return Graph{}, err
	}
	routes, err := route.BuildTable(route.TableInput{Controllers: input.Controllers, Routes: input.Routes})
	if err != nil {
		return Graph{}, err
	}
	providers, providerIndexes, err := compileProviders(input.Providers, moduleKeys)
	if err != nil {
		return Graph{}, err
	}
	components, componentIndexes, err := compileComponents(input.Components, providers, moduleKeys)
	if err != nil {
		return Graph{}, err
	}
	descriptors, err := compileDescriptors(input.Descriptors, providers, providerIndexes, moduleKeys)
	if err != nil {
		return Graph{}, err
	}
	dependencies, err := compileDependencies(input.Dependencies, components, componentIndexes, providers, providerIndexes)
	if err != nil {
		return Graph{}, err
	}
	lifecycles, err := compileLifecycles(input.Lifecycles, components, componentIndexes, providers, providerIndexes)
	if err != nil {
		return Graph{}, err
	}
	transports, err := compileTransports(input.Transports, components, componentIndexes)
	if err != nil {
		return Graph{}, err
	}
	outboxGraph, err := compileOutbox(input.Outbox, components, componentIndexes, providers, dependencies, lifecycles)
	if err != nil {
		return Graph{}, err
	}
	return Graph{
		components:   components,
		dependencies: dependencies,
		descriptors:  descriptors,
		lifecycles:   lifecycles,
		modules:      modules,
		outbox:       outboxGraph,
		providers:    providers,
		routes:       routes,
		transports:   transports,
		validated:    true,
	}, nil
}

// 构建静态模块图
func MustBuildGraph(input GraphInput) Graph {
	graph, err := BuildGraph(input)
	if err != nil {
		panic(err)
	}

	return graph
}

// 判断 Graph 是否由 BuildGraph 完整校验生成
func (g Graph) IsValidated() bool { return g.validated }

// 返回模块静态描述副本
func (g Graph) Modules() []StaticModule { return append([]StaticModule(nil), g.modules...) }

// 返回 Provider 副本
func (g Graph) Providers() []Provider { return append([]Provider(nil), g.providers...) }

// 返回组件副本
func (g Graph) Components() []Component { return append([]Component(nil), g.components...) }

// 返回组件依赖副本
func (g Graph) Dependencies() []Dependency { return append([]Dependency(nil), g.dependencies...) }

// 返回实体 Descriptor 副本
func (g Graph) Descriptors() []Descriptor { return append([]Descriptor(nil), g.descriptors...) }

// 返回生命周期定义副本
func (g Graph) Lifecycles() []Lifecycle { return append([]Lifecycle(nil), g.lifecycles...) }

// 返回 Transport 组件引用副本
func (g Graph) Transports() []Component { return append([]Component(nil), g.transports...) }

// 返回可靠消息角色图
func (g Graph) Outbox() OutboxGraph { return g.outbox.clone() }

// 返回静态路由表
func (g Graph) Routes() route.Table { return g.routes }

// 返回模块身份
func (m StaticModule) Identity() Identity { return m.identity }

// 返回展示名称
func (m StaticModule) Name() string { return m.name }

// 返回模块说明
func (m StaticModule) Description() string { return m.description }

// 返回模块排序值
func (m StaticModule) Order() int { return m.order }

// 返回模块中间件引用副本
func (m StaticModule) Middlewares() []ComponentRef {
	return append([]ComponentRef(nil), m.middlewares...)
}

// 返回全局中间件引用副本
func (m StaticModule) GlobalMiddlewares() []ComponentRef {
	return append([]ComponentRef(nil), m.globalMiddlewares...)
}

// 返回 Provider 类别
func (p Provider) Kind() ProviderKind { return p.kind }

// 返回所属模块身份键
func (p Provider) Module() string { return p.module }

// 返回声明包路径
func (p Provider) PackagePath() string { return p.packagePath }

// 返回 Provider 名称
func (p Provider) Name() string { return p.name }

// 返回 Go 类型文本
func (p Provider) Type() string { return p.typ }

// 返回所属模块身份键
func (c Component) Module() string { return c.module }

// 返回构造器包路径
func (c Component) PackagePath() string { return c.packagePath }

// 返回构造器名称
func (c Component) Name() string { return c.name }

// 已登记组件身份
func ComponentOf(definition ComponentDefinition) Component {
	return Component{module: definition.Module, packagePath: definition.PackagePath, name: definition.Name}
}

// 返回依赖方组件
func (d Dependency) Consumer() Component { return d.consumer }

// 返回构造器参数序号
func (d Dependency) ParameterIndex() int { return d.parameterIndex }

// 返回被依赖 Provider
func (d Dependency) Provider() Provider { return d.provider }

// 返回所属模块身份键
func (d Descriptor) Module() string { return d.module }

// 返回数据库表名
func (d Descriptor) Table() string { return d.table }

// 返回 Descriptor Provider
func (d Descriptor) Provider() Provider { return d.provider }

// 返回目标组件
func (l Lifecycle) Component() Component { return l.component }

// 判断组件是否需要初始化
func (l Lifecycle) HasInitializer() bool { return l.initializer }

// 判断组件是否需要启动
func (l Lifecycle) HasStarter() bool { return l.starter }

// 判断组件是否需要停止
func (l Lifecycle) HasStopper() bool { return l.stopper }

// 判断组件运行循环是否受 Host 监督
func (l Lifecycle) HasSupervisor() bool { return l.supervisor }

// 返回 Outbox Producer 组件副本
func (g OutboxGraph) Producers() []Component { return append([]Component(nil), g.producers...) }

// 返回 Consumer Definition 组件副本
func (g OutboxGraph) Consumers() []Component { return append([]Component(nil), g.consumers...) }

// 返回唯一 Publisher 组件
func (g OutboxGraph) Publisher() (Component, bool) { return optionalComponent(g.publisher) }

// 返回唯一 Consumer Adapter 组件
func (g OutboxGraph) ConsumerAdapter() (Component, bool) { return optionalComponent(g.consumerAdapter) }

// 返回唯一 Worker 生命周期组件
func (g OutboxGraph) Worker() (Component, bool) { return optionalComponent(g.worker) }

// 返回唯一 Consumer 生命周期组件
func (g OutboxGraph) ConsumerRuntime() (Component, bool) { return optionalComponent(g.consumerRuntime) }

func (g OutboxGraph) clone() OutboxGraph {
	g.producers = append([]Component(nil), g.producers...)
	g.consumers = append([]Component(nil), g.consumers...)
	return g
}

func optionalComponent(component *Component) (Component, bool) {
	if component == nil {
		return Component{}, false
	}
	return *component, true
}

func compileModules(definitions []ModuleDefinition) ([]StaticModule, map[string]bool, error) {
	keys := make(map[string]bool, len(definitions))
	modules := make([]StaticModule, len(definitions))
	for index, definition := range definitions {
		identity := Identity{key: definition.Key}
		if err := checkIdentity(identity); err != nil {
			return nil, nil, exception.WrapCore(err, "模块定义无效")
		}
		if keys[definition.Key] {
			return nil, nil, exception.Core(fmt.Sprintf("模块定义重复: %s", definition.Key))
		}
		if err := checkText("名称", definition.Name); err != nil {
			return nil, nil, exception.WrapCore(err, fmt.Sprintf("模块定义 %s 无效", definition.Key))
		}
		if err := checkText("描述", definition.Description); err != nil {
			return nil, nil, exception.WrapCore(err, fmt.Sprintf("模块定义 %s 无效", definition.Key))
		}
		if err := checkRefs("中间件", definition.Middlewares); err != nil {
			return nil, nil, exception.WrapCore(err, fmt.Sprintf("模块定义 %s 无效", definition.Key))
		}
		if err := checkRefs("全局中间件", definition.GlobalMiddlewares); err != nil {
			return nil, nil, exception.WrapCore(err, fmt.Sprintf("模块定义 %s 无效", definition.Key))
		}
		keys[definition.Key] = true
		modules[index] = StaticModule{
			description:       definition.Description,
			globalMiddlewares: append([]ComponentRef(nil), definition.GlobalMiddlewares...),
			identity:          identity,
			middlewares:       append([]ComponentRef(nil), definition.Middlewares...),
			name:              definition.Name,
			order:             definition.Order,
		}
	}

	return modules, keys, nil
}

func compileProviders(definitions []ProviderDefinition, moduleKeys map[string]bool) ([]Provider, map[providerKey]int, error) {
	providers := make([]Provider, len(definitions))
	indexes := make(map[providerKey]int, len(definitions))
	componentProviders := make(map[componentKey]bool)
	for index, definition := range definitions {
		if err := checkProvider(definition, moduleKeys); err != nil {
			return nil, nil, err
		}
		key := providerDefKey(definition)
		if _, exists := indexes[key]; exists {
			return nil, nil, exception.Core(fmt.Sprintf("Provider 重复: %s", providerLabel(definition)))
		}
		if isComponentProvider(definition.Kind) {
			component := componentDefKey(providerComponent(definition))
			if componentProviders[component] {
				return nil, nil, exception.Core(fmt.Sprintf("组件 Provider 重复: %s", componentKeyLabel(component)))
			}
			componentProviders[component] = true
		}
		indexes[key] = index
		providers[index] = providerOf(definition)
	}

	return providers, indexes, nil
}

func checkProvider(definition ProviderDefinition, moduleKeys map[string]bool) error {
	switch definition.Kind {
	case ProviderKindConfig, ProviderKindSeed, ProviderKindComponent, ProviderKindDescriptor, ProviderKindBase,
		ProviderKindEnqueuer, ProviderKindConsumerDefinition:
	default:
		return exception.Core(fmt.Sprintf("Provider 类别无效: %s", definition.Kind))
	}
	if !moduleKeys[definition.Module] {
		return exception.Core(fmt.Sprintf("Provider 所属模块不存在: %s", definition.Module))
	}
	if err := checkPkg(definition.PackagePath); err != nil {
		return exception.WrapCore(err, fmt.Sprintf("Provider %s 包路径无效", providerLabel(definition)))
	}
	if !token.IsIdentifier(definition.Name) {
		return exception.Core(fmt.Sprintf("Provider 名称无效: %s", definition.Name))
	}
	if err := checkType(definition.Type); err != nil {
		return exception.WrapCore(err, fmt.Sprintf("Provider %s 类型无效", providerLabel(definition)))
	}

	return nil
}

func compileComponents(definitions []ComponentDefinition, providers []Provider, moduleKeys map[string]bool) ([]Component, map[componentKey]int, error) {
	components := make([]Component, len(definitions))
	indexes := make(map[componentKey]int, len(definitions))
	componentProviders := make(map[componentKey]bool)
	for _, provider := range providers {
		if isComponentProvider(provider.kind) {
			componentProviders[componentKey{module: provider.module, packagePath: provider.packagePath, name: provider.name}] = true
		}
	}
	for index, definition := range definitions {
		if err := checkComponent(definition, moduleKeys); err != nil {
			return nil, nil, err
		}
		key := componentDefKey(definition)
		if _, exists := indexes[key]; exists {
			return nil, nil, exception.Core(fmt.Sprintf("组件重复: %s", componentKeyLabel(key)))
		}
		if !componentProviders[key] {
			return nil, nil, exception.Core(fmt.Sprintf("组件缺少 Provider: %s", componentKeyLabel(key)))
		}
		indexes[key] = index
		components[index] = componentFromDef(definition)
	}
	for _, provider := range providers {
		if !isComponentProvider(provider.kind) {
			continue
		}
		key := componentKey{module: provider.module, packagePath: provider.packagePath, name: provider.name}
		if _, exists := indexes[key]; !exists {
			return nil, nil, exception.Core(fmt.Sprintf("组件 Provider 缺少组件: %s", componentKeyLabel(key)))
		}
	}

	return components, indexes, nil
}

func isComponentProvider(kind ProviderKind) bool {
	return kind == ProviderKindComponent || kind == ProviderKindConsumerDefinition
}

func checkComponent(definition ComponentDefinition, moduleKeys map[string]bool) error {
	if !moduleKeys[definition.Module] {
		return exception.Core(fmt.Sprintf("组件所属模块不存在: %s", definition.Module))
	}
	if err := checkPkg(definition.PackagePath); err != nil {
		return exception.WrapCore(err, fmt.Sprintf("组件 %s 包路径无效", definition.Name))
	}
	if !token.IsIdentifier(definition.Name) {
		return exception.Core(fmt.Sprintf("组件名称无效: %s", definition.Name))
	}

	return nil
}

func compileDescriptors(definitions []DescriptorDefinition, providers []Provider, providerIndexes map[providerKey]int, moduleKeys map[string]bool) ([]Descriptor, error) {
	descriptors := make([]Descriptor, len(definitions))
	seenProviders := make(map[providerKey]bool, len(definitions))
	seenTables := make(map[string]bool, len(definitions))
	for index, definition := range definitions {
		if !moduleKeys[definition.Module] {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor 所属模块不存在: %s", definition.Module))
		}
		if definition.Provider.Kind != ProviderKindDescriptor {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor Provider 类别无效: %s", definition.Provider.Kind))
		}
		if definition.Provider.Module != definition.Module {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor 与 Provider 所属模块不一致: %s", definition.Module))
		}
		providerKey := providerDefKey(definition.Provider)
		providerIndex, exists := providerIndexes[providerKey]
		if !exists || providers[providerIndex].typ != definition.Provider.Type {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor Provider 不存在: %s", providerLabel(definition.Provider)))
		}
		if seenProviders[providerKey] {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor Provider 重复关联: %s", providerLabel(definition.Provider)))
		}
		if !tablePattern.MatchString(definition.Table) {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor 表名 %q 无效", definition.Table))
		}
		if seenTables[definition.Table] {
			return nil, exception.Core(fmt.Sprintf("实体 Descriptor 表名重复: %s", definition.Table))
		}
		seenProviders[providerKey] = true
		seenTables[definition.Table] = true
		descriptors[index] = Descriptor{
			module:   definition.Module,
			provider: providers[providerIndex],
			table:    definition.Table,
		}
	}
	for _, provider := range providers {
		if provider.kind != ProviderKindDescriptor {
			continue
		}
		key := providerValueKey(provider)
		if !seenProviders[key] {
			return nil, exception.Core(fmt.Sprintf("Descriptor Provider 缺少实体 Descriptor: %s", providerLabel(providerDef(provider))))
		}
	}

	return descriptors, nil
}

func compileDependencies(definitions []DependencyDefinition, components []Component, componentIndexes map[componentKey]int, providers []Provider, providerIndexes map[providerKey]int) ([]Dependency, error) {
	dependencies := make([]Dependency, len(definitions))
	parameterIndexes := make(map[componentKey]map[int]bool)
	for index, definition := range definitions {
		consumerKey := componentDefKey(definition.Consumer)
		consumerIndex, exists := componentIndexes[consumerKey]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("依赖方组件不存在: %s", componentKeyLabel(consumerKey)))
		}
		providerIndex, exists := providerIndexes[providerDefKey(definition.Provider)]
		if !exists || providers[providerIndex].typ != definition.Provider.Type {
			return nil, exception.Core(fmt.Sprintf("依赖 Provider 不存在: %s", providerLabel(definition.Provider)))
		}
		if definition.ParameterIndex < 0 {
			return nil, exception.Core(fmt.Sprintf("依赖参数序号无效: %d", definition.ParameterIndex))
		}
		if parameterIndexes[consumerKey] == nil {
			parameterIndexes[consumerKey] = make(map[int]bool)
		}
		if parameterIndexes[consumerKey][definition.ParameterIndex] {
			return nil, exception.Core(fmt.Sprintf("依赖参数序号重复: %s[%d]", componentKeyLabel(consumerKey), definition.ParameterIndex))
		}
		parameterIndexes[consumerKey][definition.ParameterIndex] = true
		provider := providers[providerIndex]
		if isComponentProvider(provider.kind) {
			providerComponent := componentKey{module: provider.module, packagePath: provider.packagePath, name: provider.name}
			if componentIndexes[providerComponent] >= consumerIndex {
				return nil, exception.Core(fmt.Sprintf("组件拓扑顺序无效: %s 必须先于 %s", componentKeyLabel(providerComponent), componentKeyLabel(consumerKey)))
			}
		}
		dependencies[index] = Dependency{
			consumer:       components[consumerIndex],
			parameterIndex: definition.ParameterIndex,
			provider:       provider,
		}
	}
	for _, component := range components {
		consumer := componentKey{module: component.module, packagePath: component.packagePath, name: component.name}
		indexes := parameterIndexes[consumer]
		for index := 0; index < len(indexes); index++ {
			if !indexes[index] {
				return nil, exception.Core(fmt.Sprintf("依赖参数序号不连续: %s 缺少参数 %d", componentKeyLabel(consumer), index))
			}
		}
	}
	sort.Slice(dependencies, func(left, right int) bool {
		first, second := dependencies[left], dependencies[right]
		firstKey := componentKey{module: first.consumer.module, packagePath: first.consumer.packagePath, name: first.consumer.name}
		secondKey := componentKey{module: second.consumer.module, packagePath: second.consumer.packagePath, name: second.consumer.name}
		if componentIndexes[firstKey] != componentIndexes[secondKey] {
			return componentIndexes[firstKey] < componentIndexes[secondKey]
		}
		return first.parameterIndex < second.parameterIndex
	})

	return dependencies, nil
}

func compileLifecycles(
	definitions []LifecycleDefinition,
	components []Component,
	componentIndexes map[componentKey]int,
	providers []Provider,
	providerIndexes map[providerKey]int,
) ([]Lifecycle, error) {
	byComponent := make(map[componentKey]LifecycleDefinition, len(definitions))
	for _, definition := range definitions {
		key := componentDefKey(definition.Component)
		_, exists := componentIndexes[key]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("生命周期组件不存在: %s", componentKeyLabel(key)))
		}
		if _, exists = byComponent[key]; exists {
			return nil, exception.Core(fmt.Sprintf("生命周期组件重复: %s", componentKeyLabel(key)))
		}
		providerKey := providerDefKey(ProviderDefinition{
			Kind:        ProviderKindComponent,
			Module:      definition.Component.Module,
			PackagePath: definition.Component.PackagePath,
			Name:        definition.Component.Name,
		})
		providerIndex, exists := providerIndexes[providerKey]
		if !exists {
			providerKey.kind = ProviderKindConsumerDefinition
			providerIndex, exists = providerIndexes[providerKey]
		}
		if !exists || !isComponentProvider(providers[providerIndex].kind) {
			return nil, exception.Core(fmt.Sprintf("生命周期组件 Provider 不存在: %s", componentKeyLabel(key)))
		}
		byComponent[key] = definition
	}
	if len(definitions) != len(components) {
		return nil, exception.Core("生命周期定义必须覆盖全部组件")
	}

	lifecycles := make([]Lifecycle, 0, len(components))
	for _, component := range components {
		key := componentValueKey(component)
		definition, exists := byComponent[key]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("组件缺少生命周期定义: %s", componentKeyLabel(key)))
		}
		keyByProvider := providerKey{
			kind:        ProviderKindComponent,
			module:      component.module,
			packagePath: component.packagePath,
			name:        component.name,
		}
		providerIndex, exists := providerIndexes[keyByProvider]
		if !exists {
			keyByProvider.kind = ProviderKindConsumerDefinition
			providerIndex = providerIndexes[keyByProvider]
		}
		provider := providers[providerIndex]
		lifecycles = append(lifecycles, Lifecycle{
			component:   component,
			initializer: definition.Initializer,
			provider:    provider,
			starter:     definition.Starter,
			stopper:     definition.Stopper,
			supervisor:  definition.Supervisor,
		})
	}
	return lifecycles, nil
}

func compileTransports(definitions []ComponentDefinition, components []Component, componentIndexes map[componentKey]int) ([]Component, error) {
	transports := make([]Component, len(definitions))
	seen := make(map[componentKey]bool, len(definitions))
	for index, definition := range definitions {
		key := componentDefKey(definition)
		componentIndex, exists := componentIndexes[key]
		if !exists {
			return nil, exception.Core(fmt.Sprintf("Transport 组件不存在: %s", componentKeyLabel(key)))
		}
		if seen[key] {
			return nil, exception.Core(fmt.Sprintf("Transport 组件重复: %s", componentKeyLabel(key)))
		}
		seen[key] = true
		transports[index] = components[componentIndex]
	}
	sort.Slice(transports, func(left, right int) bool {
		return componentIndexes[componentValueKey(transports[left])] < componentIndexes[componentValueKey(transports[right])]
	})
	return transports, nil
}

func checkPkg(packagePath string) error {
	if strings.TrimSpace(packagePath) == "" || strings.TrimSpace(packagePath) != packagePath {
		return exception.Core("包路径不能为空且不能包含首尾空白")
	}
	for _, segment := range strings.Split(packagePath, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return exception.Core("包路径包含无效路径段")
		}
		for _, character := range segment {
			if unicode.IsControl(character) || unicode.IsSpace(character) {
				return exception.Core("包路径不能包含空白或控制字符")
			}
		}
	}

	return nil
}

func checkType(typ string) error {
	if strings.TrimSpace(typ) == "" || strings.TrimSpace(typ) != typ {
		return exception.Core("类型文本不能为空且不能包含首尾空白")
	}
	for _, character := range typ {
		if unicode.IsControl(character) {
			return exception.Core("类型文本不能包含控制字符")
		}
	}

	return nil
}

func providerComponent(definition ProviderDefinition) ComponentDefinition {
	return ComponentDefinition{Module: definition.Module, PackagePath: definition.PackagePath, Name: definition.Name}
}

func componentFromDef(definition ComponentDefinition) Component {
	return Component{module: definition.Module, packagePath: definition.PackagePath, name: definition.Name}
}

func componentDefKey(definition ComponentDefinition) componentKey {
	return componentKey{module: definition.Module, packagePath: definition.PackagePath, name: definition.Name}
}

func componentValueKey(component Component) componentKey {
	return componentKey{module: component.module, packagePath: component.packagePath, name: component.name}
}

func componentKeyLabel(key componentKey) string {
	return key.module + ":" + key.packagePath + ":" + key.name
}

func providerOf(definition ProviderDefinition) Provider {
	return Provider{
		kind:        definition.Kind,
		module:      definition.Module,
		packagePath: definition.PackagePath,
		name:        definition.Name,
		typ:         definition.Type,
	}
}

func providerDef(provider Provider) ProviderDefinition {
	return ProviderDefinition{
		Kind:        provider.kind,
		Module:      provider.module,
		PackagePath: provider.packagePath,
		Name:        provider.name,
		Type:        provider.typ,
	}
}

func providerDefKey(definition ProviderDefinition) providerKey {
	return providerKey{
		kind:        definition.Kind,
		module:      definition.Module,
		packagePath: definition.PackagePath,
		name:        definition.Name,
	}
}

func providerValueKey(provider Provider) providerKey {
	return providerKey{
		kind:        provider.kind,
		module:      provider.module,
		packagePath: provider.packagePath,
		name:        provider.name,
	}
}

func providerLabel(definition ProviderDefinition) string {
	return string(definition.Kind) + ":" + definition.Module + ":" + definition.PackagePath + ":" + definition.Name + ":" + definition.Type
}
