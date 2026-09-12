package codegen

import (
	"go/ast"
	"go/types"

	"github.com/toothdy/cool-admin-go-next/cool-next/core/module"
	"github.com/toothdy/cool-admin-go-next/cool-next/core/route"
)

// 源码位置
type Position struct {
	File   string // 相对工作区的文件
	Line   int    // 行号
	Column int    // 列号
}

// 模块配置声明
type ConfigDeclaration struct {
	description string     // 模块说明
	name        string     // 模块名称
	packageName string     // 所在包名称
	packagePath string     // 所在包路径
	typeName    string     // 配置类型名称
	position    Position   // 声明位置
	typ         types.Type // 配置类型对象
	order       int        // 模块排序值
}

// 返回配置类型名称
func (d ConfigDeclaration) TypeName() string { return d.typeName }

// 返回声明位置
func (d ConfigDeclaration) Position() Position { return d.position }

// 返回模块排序值
func (d ConfigDeclaration) Order() int { return d.order }

// 实体声明
type EntityDeclaration struct {
	name        string        // 实体名称
	position    Position      // 实体声明位置
	packageName string        // 实体包名称
	packagePath string        // 实体包路径
	fields      []entityField // 直接声明字段
	typ         *types.Named  // 实体类型
}

// 返回实体名称
func (d EntityDeclaration) Name() string { return d.name }

// 返回声明位置
func (d EntityDeclaration) Position() Position { return d.position }

type entityField struct {
	embedded bool       // 是否匿名嵌入
	position Position   // 字段声明位置
	tag      string     // 原始结构体标签
	typ      types.Type // 字段类型
	variable *types.Var // 字段对象
}

// Schema 函数声明
type SchemaDeclaration struct {
	entity   string        // 目标实体名称
	name     string        // 函数名称
	position Position      // 声明位置
	source   *schemaSource // 源码位置
}

// 返回目标实体名称
func (d SchemaDeclaration) Entity() string { return d.entity }

// 返回函数名称
func (d SchemaDeclaration) Name() string { return d.name }

// 返回声明位置
func (d SchemaDeclaration) Position() Position { return d.position }

type schemaSource struct {
	dir      string
	function *ast.FuncDecl
	pkg      *loadedPackage
}

// 构造器声明
type Constructor struct {
	consumerMessageType string       // Consumer 消息契约类型
	consumerName        string       // Consumer 稳定名称
	consumerTopic       string       // Consumer 消息目的地
	consumerVersions    []uint32     // Consumer 支持版本
	hasError            bool         // 是否返回 error
	hasInitializer      bool         // 是否实现初始化契约
	isConsumerAdapter   bool         // 是否提供 Consumer Adapter
	isConsumerDef       bool         // 是否声明 Consumer Definition
	isProducer          bool         // 是否直接依赖 Enqueuer
	isPublisher         bool         // 是否提供 Publisher
	hasStarter          bool         // 是否实现启动契约
	hasStopper          bool         // 是否实现停止契约
	hasTransport        bool         // 是否实现 Transport 契约
	name                string       // 构造器名称
	packageName         string       // 所在包名称
	packagePath         string       // 所在包路径
	paramDecls          []Position   // 参数类型声明位置
	paramPos            []Position   // 参数源码位置
	parameters          []string     // 参数类型列表
	position            Position     // 声明位置
	result              string       // 返回类型名称
	resultType          types.Type   // 返回类型
	types               []types.Type // 类型列表，用于类型检查
}

// 返回构造器名称
func (c Constructor) Name() string { return c.name }

// 返回参数类型副本
func (c Constructor) Parameters() []string { return append([]string(nil), c.parameters...) }

// 返回返回类型
func (c Constructor) Result() string { return c.result }

// 返回声明位置
func (c Constructor) Position() Position { return c.position }

// 判断返回组件是否需要初始化
func (c Constructor) HasInitializer() bool { return c.hasInitializer }

// 判断返回组件是否需要启动
func (c Constructor) HasStarter() bool { return c.hasStarter }

// 判断返回组件是否需要停止
func (c Constructor) HasStopper() bool { return c.hasStopper }

// 判断返回组件是否为 Transport
func (c Constructor) HasTransport() bool { return c.hasTransport }

// gRPC 服务注册函数声明
type GRPCRegistrarDeclaration struct {
	name        string   // 函数名称
	packageName string   // 所在包名称
	packagePath string   // 所在包路径
	position    Position // 声明位置
}

// 返回注册函数名称
func (d GRPCRegistrarDeclaration) Name() string { return d.name }

// 返回注册函数包路径
func (d GRPCRegistrarDeclaration) PackagePath() string { return d.packagePath }

// 返回声明位置
func (d GRPCRegistrarDeclaration) Position() Position { return d.position }

// Service 动作模式
type ServiceActionMode string

const (
	ServiceActionBase     ServiceActionMode = "base"     // 基础实现
	ServiceActionOverride ServiceActionMode = "override" // 完全覆盖
	ServiceActionDelegate ServiceActionMode = "delegate" // 委托基础实现
)

// Service 动作声明
type ServiceAction struct {
	mode     ServiceActionMode
	name     string
	position Position
}

// 返回动作名称
func (a ServiceAction) Name() string { return a.name }

// 返回动作模式
func (a ServiceAction) Mode() ServiceActionMode { return a.mode }

// 返回动作声明位置
func (a ServiceAction) Position() Position { return a.position }

// Base Service 声明
type ServiceDeclaration struct {
	actions     []ServiceAction
	entityType  types.Type
	hasAfter    bool
	hasBefore   bool
	idType      types.Type
	name        string
	packageName string
	packagePath string
	position    Position
	typ         *types.Named
}

// 返回 Service 名称
func (d ServiceDeclaration) Name() string { return d.name }

// 返回 Service 包路径
func (d ServiceDeclaration) PackagePath() string { return d.packagePath }

// 返回 Service 声明位置
func (d ServiceDeclaration) Position() Position { return d.position }

// 返回动作分析结果副本
func (d ServiceDeclaration) Actions() []ServiceAction {
	return append([]ServiceAction(nil), d.actions...)
}

// 判断是否声明修改前 Hook
func (d ServiceDeclaration) HasModifyBefore() bool { return d.hasBefore }

// 判断是否声明修改后 Hook
func (d ServiceDeclaration) HasModifyAfter() bool { return d.hasAfter }

// Controller 区域
type ControllerArea string

const (
	ControllerAdmin ControllerArea = "admin" // 后台区域
	ControllerApp   ControllerArea = "app"   // 应用端区域
)

// Controller 工厂声明
type ControllerDeclaration struct {
	aliases            []string           // Controller 别名
	area               ControllerArea     // Controller 区域
	description        string             // Controller 描述
	developmentOnly    bool               // 是否仅在开发环境注册
	entityType         types.Type         // Curd 实体类型
	hasCurd            bool               // 是否声明默认 CRUD
	ignoreGlobalPrefix bool               // 是否忽略全局前缀
	insertType         types.Type         // InsertParam 实体类型
	middleware         []string           // Controller 中间件
	name               string             // 工厂函数名称
	packageName        string             // 所在包名称
	packagePath        string             // 所在包路径
	parameterTypes     []types.Type       // 工厂参数类型
	path               string             // Controller 完整路径
	position           Position           // 工厂声明位置
	prefix             string             // 默认 CRUD 完整路径
	routes             []RouteDeclaration // 静态路由
	sensitive          bool               // 路径是否大小写敏感
	serviceType        types.Type         // Curd Service 类型
	tagName            string             // Controller 标签名
}

// 返回 Controller 区域
func (d ControllerDeclaration) Area() ControllerArea { return d.area }

// 返回工厂函数名称
func (d ControllerDeclaration) Name() string { return d.name }

// 返回 Controller 完整路径
func (d ControllerDeclaration) Path() string { return d.path }

// 返回默认 CRUD 完整路径
func (d ControllerDeclaration) Prefix() string { return d.prefix }

// 返回工厂声明位置
func (d ControllerDeclaration) Position() Position { return d.position }

// 判断是否声明默认 CRUD
func (d ControllerDeclaration) HasCurd() bool { return d.hasCurd }

// 返回 Controller 别名副本
func (d ControllerDeclaration) Aliases() []string { return append([]string(nil), d.aliases...) }

// 返回 Controller 中间件副本
func (d ControllerDeclaration) Middleware() []string {
	return append([]string(nil), d.middleware...)
}

// 仅在开发环境注册
func (d ControllerDeclaration) DevelopmentOnly() bool { return d.developmentOnly }

// 返回静态路由副本
func (d ControllerDeclaration) Routes() []RouteDeclaration {
	result := append([]RouteDeclaration(nil), d.routes...)
	for index := range result {
		result[index].middleware = append([]string(nil), result[index].middleware...)
		result[index].tags = append([]string(nil), result[index].tags...)
	}

	return result
}

// 静态路由声明
type RouteDeclaration struct {
	bind            route.BindSource
	description     string
	developmentOnly bool
	handler         route.CallableRef
	kind            route.Kind
	method          string
	middleware      []string
	path            string
	position        Position
	summary         string
	tags            []string
	transaction     route.TransactionPolicy
}

// 返回路由种类
func (d RouteDeclaration) Kind() route.Kind { return d.kind }

// 返回 HTTP Method
func (d RouteDeclaration) Method() string { return d.method }

// 返回完整路径
func (d RouteDeclaration) Path() string { return d.path }

// 返回绑定来源
func (d RouteDeclaration) Bind() route.BindSource { return d.bind }

// 返回权限字符串

// 返回标签副本
func (d RouteDeclaration) Tags() []string { return append([]string(nil), d.tags...) }

// 返回中间件副本
func (d RouteDeclaration) Middleware() []string {
	return append([]string(nil), d.middleware...)
}

// 返回声明位置
func (d RouteDeclaration) Position() Position { return d.position }

// 仅在开发环境注册
func (d RouteDeclaration) DevelopmentOnly() bool { return d.developmentOnly }

// 静态组件引用
type Reference struct {
	group    string   // 数据库组
	position Position // 引用位置
	symbol   string   // 原始符号路径
	target   Position // 目标位置
}

// 返回所属声明字段
func (r Reference) Group() string { return r.group }

// 返回原始符号路径
func (r Reference) Symbol() string { return r.symbol }

// 返回引用位置
func (r Reference) Position() Position { return r.position }

// 返回目标位置
func (r Reference) Target() Position { return r.target }

// 模块分析结果
type Module struct {
	identity     module.Identity            // 模块身份
	config       ConfigDeclaration          // 模块配置声明
	constructors []Constructor              // 构造器声明
	controllers  []ControllerDeclaration    // Controller 工厂声明
	entities     []EntityDeclaration        // 实体声明
	registrars   []GRPCRegistrarDeclaration // gRPC 服务注册函数
	references   []Reference                // 静态组件引用
	root         string                     // 工作区相对模块根目录
	seedDB       bool                       // 模块根存在 db.json
	seedMenu     bool                       // 模块根存在 menu.json
	schemas      []SchemaDeclaration        // Schema 声明
	services     []ServiceDeclaration       // Base Service 声明
}

// 返回模块身份
func (m Module) Identity() module.Identity { return m.identity }

// 返回模块配置声明
func (m Module) Config() ConfigDeclaration { return m.config }

// 模块是否包含数据库种子
func (m Module) HasSeedDB() bool { return m.seedDB }

// 模块是否包含菜单种子
func (m Module) HasSeedMenu() bool { return m.seedMenu }

// 返回实体声明副本
func (m Module) Entities() []EntityDeclaration {
	return append([]EntityDeclaration(nil), m.entities...)
}

// 返回 Schema 声明副本
func (m Module) Schemas() []SchemaDeclaration { return append([]SchemaDeclaration(nil), m.schemas...) }

// 返回构造器声明副本
func (m Module) Constructors() []Constructor {
	result := append([]Constructor(nil), m.constructors...)
	for index := range result {
		result[index].consumerVersions = append([]uint32(nil), result[index].consumerVersions...)
		result[index].paramDecls = append([]Position(nil), result[index].paramDecls...)
		result[index].paramPos = append([]Position(nil), result[index].paramPos...)
		result[index].parameters = append([]string(nil), result[index].parameters...)
		result[index].types = append([]types.Type(nil), result[index].types...)
	}
	return result
}

// 返回 gRPC 服务注册函数副本
func (m Module) GRPCRegistrars() []GRPCRegistrarDeclaration {
	return append([]GRPCRegistrarDeclaration(nil), m.registrars...)
}

// 返回 Controller 工厂声明副本
func (m Module) Controllers() []ControllerDeclaration {
	result := append([]ControllerDeclaration(nil), m.controllers...)
	for index := range result {
		result[index].aliases = append([]string(nil), result[index].aliases...)
		result[index].middleware = append([]string(nil), result[index].middleware...)
		result[index].parameterTypes = append([]types.Type(nil), result[index].parameterTypes...)
		result[index].routes = result[index].Routes()
	}

	return result
}

// 返回静态组件引用副本
func (m Module) References() []Reference { return append([]Reference(nil), m.references...) }

// 返回 Base Service 声明副本
func (m Module) Services() []ServiceDeclaration {
	result := append([]ServiceDeclaration(nil), m.services...)
	for index := range result {
		result[index].actions = append([]ServiceAction(nil), result[index].actions...)
	}

	return result
}

// 不可变分析模型
type Model struct {
	diagnostics []Diagnostic
	modules     []Module
}

// 返回模块副本
func (m *Model) Modules() []Module {
	if m == nil {
		return nil
	}
	return append([]Module(nil), m.modules...)
}

// 返回诊断副本
func (m *Model) Diagnostics() []Diagnostic {
	if m == nil {
		return nil
	}
	return append([]Diagnostic(nil), m.diagnostics...)
}
