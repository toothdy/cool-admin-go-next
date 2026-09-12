package codegen

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
)

// 依赖图 Provider 节点
type Provider struct {
	kind        ProviderKind
	name        string
	module      string
	packagePath string
	typ         string
}

// Provider 类别
type ProviderKind string

const (
	ProviderKindConfig             ProviderKind = "config"          // 模块配置
	ProviderKindSeed               ProviderKind = "seed"            // 模块种子数据
	ProviderKindComponent          ProviderKind = "component"       // 普通构造器
	ProviderKindDescriptor         ProviderKind = "descriptor"      // 实体 Descriptor
	ProviderKindBase               ProviderKind = "base"            // 实体 Base Service
	ProviderKindEnqueuer           ProviderKind = "outbox-enqueuer" // Outbox Enqueuer
	ProviderKindConsumerDefinition ProviderKind = "outbox-consumer" // Consumer Definition
)

// 返回 Provider 类别
func (p Provider) Kind() ProviderKind { return p.kind }

// 返回 Provider 名称
func (p Provider) Name() string { return p.name }

// 返回模块身份键
func (p Provider) Module() string { return p.module }

// 返回 Provider 声明包路径
func (p Provider) PackagePath() string { return p.packagePath }

// 返回类型文本
func (p Provider) Type() string { return p.typ }

// 组件节点
type Component struct {
	name        string
	module      string
	packagePath string
	position    Position
}

// 返回构造器名称
func (c Component) Name() string { return c.name }

// 返回模块身份键
func (c Component) Module() string { return c.module }

// 返回构造器包路径
func (c Component) PackagePath() string { return c.packagePath }

// 返回声明位置
func (c Component) Position() Position { return c.position }

// 模块图节点
type ModuleNode struct {
	key   string
	order int
}

// 返回模块身份键
func (n ModuleNode) Key() string { return n.key }

// 返回模块排序值
func (n ModuleNode) Order() int { return n.order }

// 组件到 Provider 的依赖边
type Dependency struct {
	consumer       Component
	parameterIndex int
	position       Position
	provider       Provider
}

// 返回依赖方组件
func (d Dependency) Consumer() Component { return d.consumer }

// 返回依赖参数位置
func (d Dependency) Position() Position { return d.position }

// 返回依赖参数序号
func (d Dependency) ParameterIndex() int { return d.parameterIndex }

// 返回被依赖 Provider
func (d Dependency) Provider() Provider { return d.provider }

// 跨模块依赖边
type ModuleDependency struct {
	consumer string
	position Position
	provider string
}

// 返回依赖方模块
func (d ModuleDependency) Consumer() string { return d.consumer }

// 返回依赖参数位置
func (d ModuleDependency) Position() Position { return d.position }

// 返回被依赖模块
func (d ModuleDependency) Provider() string { return d.provider }

// 不可变 Provider 图
type Graph struct {
	consumerAdapter    *Component
	consumers          []Component
	components         []Component
	dependencies       []Dependency
	moduleDependencies []ModuleDependency
	modules            []ModuleNode
	order              []Component
	producers          []Component
	providers          []Provider
	publisher          *Component
}

type graphProvider struct {
	componentIndex int
	provider       Provider
	typ            types.Type
}

type graphComponent struct {
	component   Component
	constructor Constructor
	index       int
	module      Module
}

type graphDependency struct {
	consumer          int
	parameterIndex    int
	position          Position
	provider          int
	providerComponent int
}

// 构建不含实体 Provider 的依赖图
func BuildGraph(model *Model) (*Graph, error) {
	if model == nil {
		return nil, graphError("CG030", "分析模型不能为空", Position{})
	}
	for _, current := range model.modules {
		if len(current.entities) > 0 {
			return nil, graphError("CG038", "含实体的分析模型必须使用 BuildGraphWithDescriptors", current.entities[0].position)
		}
	}
	return buildGraph(model, nil)
}

// 构建包含 Descriptor Provider 的依赖图
func BuildGraphWithDescriptors(model *Model, descriptors *DescriptorSet) (*Graph, error) {
	if model == nil {
		return nil, graphError("CG030", "分析模型不能为空", Position{})
	}
	if descriptors == nil {
		return nil, graphError("CG038", "Descriptor 集合不能为空", Position{})
	}
	if err := checkDescriptors(model, descriptors); err != nil {
		return nil, err
	}
	return buildGraph(model, descriptors)
}

func buildGraph(model *Model, descriptors *DescriptorSet) (*Graph, error) {
	graph := &Graph{}
	modules := make(map[string]Module, len(model.modules))
	var (
		nodes     []graphComponent
		providers []graphProvider
	)
	for _, current := range model.modules {
		key := current.identity.Key()
		modules[key] = current
		graph.modules = append(graph.modules, ModuleNode{key: key, order: current.config.order})
		providers = append(providers, graphProvider{
			componentIndex: -1,
			provider:       Provider{kind: ProviderKindConfig, name: current.config.typeName, module: key, packagePath: current.config.packagePath, typ: types.TypeString(current.config.typ, qualifier)},
			typ:            current.config.typ,
		})
	}
	if seedType := findSeedDataType(model); seedType != nil {
		for _, current := range model.modules {
			providers = append(providers, graphProvider{componentIndex: -1, provider: Provider{kind: ProviderKindSeed, name: "Data", module: current.identity.Key(), packagePath: seedPackagePath, typ: types.TypeString(seedType, qualifier)}, typ: seedType})
		}
	}
	// 框架数据库 Runtime 作为共享 Provider，由生成的装配局部变量 runtime 提供
	if runtimeType := runtimeProviderType(model); runtimeType != nil {
		providers = append(providers, graphProvider{
			componentIndex: -1,
			provider: Provider{
				kind:        ProviderKindComponent,
				name:        "Runtime",
				module:      frameworkModuleKey,
				packagePath: databasePackagePath,
				typ:         types.TypeString(runtimeType, qualifier),
			},
			typ: runtimeType,
		})
	}
	// 框架回收站 Store 由数据库 Runtime 同步创建并共享
	if storeType := recycleProviderType(model); storeType != nil {
		providers = append(providers, graphProvider{
			componentIndex: -1,
			provider: Provider{
				kind:        ProviderKindComponent,
				name:        "Store",
				module:      frameworkModuleKey,
				packagePath: recyclePackagePath,
				typ:         types.TypeString(storeType, qualifier),
			},
			typ: storeType,
		})
	}
	providers = addAuthProviders(model, providers)
	for _, current := range model.modules {
		seen := make([]types.Type, 0, len(current.constructors))
		for _, constructor := range current.constructors {
			for _, registered := range seen {
				if !constructor.isConsumerDef && types.Identical(types.Unalias(registered), types.Unalias(constructor.resultType)) {
					return nil, graphError("CG031", "存在重复 Provider: "+constructor.result, constructor.position)
				}
			}
			if !constructor.isConsumerDef {
				seen = append(seen, constructor.resultType)
			}
			component := Component{name: constructor.name, module: current.identity.Key(), packagePath: constructor.packagePath, position: constructor.position}
			nodeIndex := len(nodes)
			nodes = append(nodes, graphComponent{component: component, constructor: constructor, index: nodeIndex, module: current})
			providerKind := ProviderKindComponent
			if constructor.isConsumerDef {
				providerKind = ProviderKindConsumerDefinition
				graph.consumers = append(graph.consumers, component)
			}
			if constructor.isProducer {
				graph.producers = append(graph.producers, component)
			}
			if constructor.isPublisher {
				if graph.publisher != nil {
					return nil, graphError("CG107", "应用图只能提供一个 Publisher", constructor.position)
				}
				currentComponent := component
				graph.publisher = &currentComponent
			}
			if constructor.isConsumerAdapter {
				if graph.consumerAdapter != nil {
					return nil, graphError("CG108", "应用图只能提供一个 Consumer Adapter", constructor.position)
				}
				currentComponent := component
				graph.consumerAdapter = &currentComponent
			}
			providers = append(providers, graphProvider{
				componentIndex: nodeIndex,
				provider:       Provider{kind: providerKind, name: constructor.name, module: current.identity.Key(), packagePath: constructor.packagePath, typ: constructor.result},
				typ:            constructor.resultType,
			})
		}
	}
	if len(graph.producers) > 0 {
		if graph.publisher == nil {
			return nil, graphError("CG109", "Outbox Producer 缺少 Publisher", graph.producers[0].position)
		}
		providers = append(providers, graphProvider{
			componentIndex: -1,
			provider: Provider{
				kind:        ProviderKindEnqueuer,
				name:        "Enqueuer",
				module:      ".framework",
				packagePath: outboxPackagePath,
				typ:         outboxPackagePath + ".Enqueuer",
			},
			typ: enqueuerType(nodes),
		})
	}
	if len(graph.consumers) > 0 && graph.consumerAdapter == nil {
		return nil, graphError("CG110", "可靠 Consumer 缺少 Consumer Adapter", graph.consumers[0].position)
	}
	if descriptors != nil {
		for _, fragment := range descriptors.fragments {
			candidate := fragment.provider
			if _, exists := modules[candidate.module]; !exists {
				return nil, graphError("CG039", "Descriptor Provider 所属模块不存在", candidate.position)
			}
			providers = append(providers, graphProvider{
				componentIndex: -1,
				provider:       Provider{kind: ProviderKindDescriptor, name: candidate.name, module: candidate.module, packagePath: fragment.entityPackage, typ: candidate.typ},
				typ:            candidate.typObject,
			})
			baseCandidate := fragment.baseProvider
			providers = append(providers, graphProvider{
				componentIndex: -1,
				provider:       Provider{kind: ProviderKindBase, name: baseCandidate.name, module: baseCandidate.module, packagePath: fragment.entityPackage, typ: baseCandidate.typ},
				typ:            baseProviderType(model, fragment.entityType),
			})
		}
	}

	dependencies, err := resolveDeps(nodes, providers, modules)
	if err != nil {
		return nil, err
	}
	graph.dependencies = exportDeps(dependencies, nodes, providers)
	graph.moduleDependencies = moduleDeps(dependencies, nodes, providers)
	if cycle, position := findModuleCycle(graph.modules, graph.moduleDependencies); len(cycle) > 0 {
		return nil, graphError("CG036", "模块依赖循环: "+strings.Join(cycle, " -> "), position)
	}
	if cycle, position := findComponentCycle(nodes, dependencies); len(cycle) > 0 {
		return nil, graphError("CG034", "组件依赖循环: "+strings.Join(cycle, " -> "), position)
	}

	graph.order = topologicalOrder(nodes, dependencies, graph.modules)
	for _, node := range nodes {
		graph.components = append(graph.components, node.component)
	}
	sort.Slice(graph.components, func(left, right int) bool {
		return compareComponents(graph.components[left], graph.components[right], graph.modules) < 0
	})
	for _, provider := range providers {
		graph.providers = append(graph.providers, provider.provider)
	}
	sort.Slice(graph.providers, func(left, right int) bool {
		first, second := graph.providers[left], graph.providers[right]
		if first.module != second.module {
			return first.module < second.module
		}
		if first.packagePath != second.packagePath {
			return first.packagePath < second.packagePath
		}
		if first.name != second.name {
			return first.name < second.name
		}
		return first.typ < second.typ
	})
	sort.Slice(graph.modules, func(left, right int) bool { return graph.modules[left].key < graph.modules[right].key })
	return graph, nil
}

// 由生成装配负责创建的认证端口
func addAuthProviders(model *Model, providers []graphProvider) []graphProvider {
	seen := make(map[string]bool)
	for _, current := range providers {
		seen[current.provider.packagePath+"."+current.provider.name] = true
	}
	for _, current := range model.modules {
		for _, constructor := range current.constructors {
			for _, parameter := range constructor.types {
				name, packagePath := authProviderType(parameter)
				key := packagePath + "." + name
				if name == "" || seen[key] {
					continue
				}
				seen[key] = true
				providers = append(providers, graphProvider{
					componentIndex: -1,
					provider:       Provider{kind: ProviderKindComponent, name: name, module: frameworkModuleKey, packagePath: packagePath, typ: types.TypeString(parameter, qualifier)},
					typ:            parameter,
				})
			}
		}
	}
	return providers
}

func authProviderType(value types.Type) (string, string) {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return "", ""
	}
	path := named.Obj().Pkg().Path()
	if path == authPackagePath {
		switch named.Obj().Name() {
		case "Service", "Codec", "Store":
			return named.Obj().Name(), path
		}
	}
	if path == riskPackagePath && named.Obj().Name() == "Service" {
		return named.Obj().Name(), path
	}
	if path == bcryptPath && named.Obj().Name() == "Verifier" {
		return named.Obj().Name(), path
	}
	return "", ""
}

func baseProviderType(model *Model, entityType types.Type) types.Type {
	for _, current := range model.modules {
		for _, constructor := range current.constructors {
			for _, parameter := range constructor.types {
				if isBaseProvider(parameter, entityType) {
					return parameter
				}
			}
		}
	}

	return nil
}

func findSeedDataType(model *Model) types.Type {
	for _, current := range model.modules {
		for _, constructor := range current.constructors {
			for _, parameter := range constructor.types {
				named, ok := types.Unalias(parameter).(*types.Named)
				if ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == seedPackagePath && named.Obj().Name() == "Data" {
					return parameter
				}
			}
		}
	}

	return nil
}

// 任意构造器参数中的 *db.Runtime 类型，返回其 types.Type 对象
func runtimeProviderType(model *Model) types.Type {
	return frameworkType(model, databasePackagePath, "Runtime")
}

// 任意构造器参数中的 *gnrecycle.Store 类型，返回其 types.Type 对象
func recycleProviderType(model *Model) types.Type {
	return frameworkType(model, recyclePackagePath, "Store")
}

func frameworkType(model *Model, packagePath, name string) types.Type {
	for _, current := range model.modules {
		for _, constructor := range current.constructors {
			for _, parameter := range constructor.types {
				parameter = types.Unalias(parameter)
				pointer, matches := parameter.(*types.Pointer)
				if !matches {
					continue
				}
				named, matches := types.Unalias(pointer.Elem()).(*types.Named)
				if !matches || named.Obj() == nil || named.Obj().Pkg() == nil ||
					named.Obj().Pkg().Path() != packagePath || named.Obj().Name() != name {
					continue
				}
				return parameter
			}
		}
	}
	return nil
}

func isBaseProvider(value, entityType types.Type) bool {
	pointer, matches := types.Unalias(value).(*types.Pointer)
	if !matches {
		return false
	}
	named, matches := types.Unalias(pointer.Elem()).(*types.Named)
	if !matches || named.Obj() == nil || named.Obj().Pkg() == nil ||
		named.Obj().Pkg().Path() != servicePackagePath || named.Obj().Name() != "Base" {
		return false
	}
	arguments := named.TypeArgs()
	return arguments.Len() == 2 && types.Identical(arguments.At(0), entityType) &&
		types.Identical(arguments.At(1), types.Typ[types.Uint64])
}

func checkDescriptors(model *Model, descriptors *DescriptorSet) error {
	expected := make(map[string]Position)
	for _, current := range model.modules {
		for _, entity := range current.entities {
			expected[graphDescKey(current.identity.Key(), entity.packagePath, entity.name)] = entity.position
		}
	}
	seen := make(map[string]bool, len(descriptors.fragments))
	for _, fragment := range descriptors.fragments {
		key := graphDescKey(fragment.module, fragment.entityPackage, fragment.entity)
		position, exists := expected[key]
		if !exists {
			return graphError("CG039", "Descriptor Provider 不属于已发现实体", fragment.provider.position)
		}
		if seen[key] {
			return graphError("CG039", "实体存在重复 Descriptor Provider", position)
		}
		seen[key] = true
	}
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !seen[key] {
			return graphError("CG039", "实体缺少 Descriptor Provider", expected[key])
		}
	}
	return nil
}

func graphDescKey(module, packagePath, entity string) string {
	return module + ":" + packagePath + ":" + entity
}

// 返回 Provider 副本
func (g *Graph) Providers() []Provider {
	if g == nil {
		return nil
	}
	return append([]Provider(nil), g.providers...)
}

// 返回组件副本
func (g *Graph) Components() []Component {
	if g == nil {
		return nil
	}
	return append([]Component(nil), g.components...)
}

// 返回模块副本
func (g *Graph) Modules() []ModuleNode {
	if g == nil {
		return nil
	}
	return append([]ModuleNode(nil), g.modules...)
}

// 返回构造顺序副本
func (g *Graph) Order() []Component {
	if g == nil {
		return nil
	}
	return append([]Component(nil), g.order...)
}

// 返回组件依赖边副本
func (g *Graph) Dependencies() []Dependency {
	if g == nil {
		return nil
	}
	return append([]Dependency(nil), g.dependencies...)
}

// 返回模块依赖边副本
func (g *Graph) ModuleDependencies() []ModuleDependency {
	if g == nil {
		return nil
	}
	return append([]ModuleDependency(nil), g.moduleDependencies...)
}

// 返回 Outbox Producer 组件副本
func (g *Graph) Producers() []Component {
	if g == nil {
		return nil
	}
	return append([]Component(nil), g.producers...)
}

// 返回 Consumer Definition 组件副本
func (g *Graph) Consumers() []Component {
	if g == nil {
		return nil
	}
	return append([]Component(nil), g.consumers...)
}

// 返回唯一 Publisher 组件
func (g *Graph) Publisher() (Component, bool) {
	if g == nil || g.publisher == nil {
		return Component{}, false
	}
	return *g.publisher, true
}

// 返回唯一 Consumer Adapter 组件
func (g *Graph) ConsumerAdapter() (Component, bool) {
	if g == nil || g.consumerAdapter == nil {
		return Component{}, false
	}
	return *g.consumerAdapter, true
}

func enqueuerType(nodes []graphComponent) types.Type {
	for _, node := range nodes {
		if !node.constructor.isProducer {
			continue
		}
		for _, parameter := range node.constructor.types {
			if isOutboxType(parameter, "Enqueuer") {
				return parameter
			}
		}
	}
	return nil
}

func compareComponents(left, right Component, modules []ModuleNode) int {
	orders := make(map[string]int, len(modules))
	for _, current := range modules {
		orders[current.key] = current.order
	}
	if orders[left.module] != orders[right.module] {
		if orders[left.module] > orders[right.module] {
			return -1
		}
		return 1
	}
	if left.module != right.module {
		return strings.Compare(left.module, right.module)
	}
	if left.packagePath != right.packagePath {
		return strings.Compare(left.packagePath, right.packagePath)
	}
	return strings.Compare(left.name, right.name)
}

func graphError(code, message string, position Position) error {
	return &DiagnosticError{diagnostics: []Diagnostic{{Code: code, Message: message, Position: position}}}
}

func providerLabel(provider graphProvider) string {
	return fmt.Sprintf("%s.%s", provider.provider.module, provider.provider.name)
}

func componentLabel(component Component, nodes []graphComponent) string {
	for _, node := range nodes {
		candidate := node.component
		if candidate.module == component.module && candidate.name == component.name && candidate.packagePath != component.packagePath {
			return component.packagePath + "." + component.name
		}
	}
	return component.module + "." + component.name
}
