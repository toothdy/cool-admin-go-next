package codegen

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

const outboxPackagePath = "github.com/toothdy/cool-admin-go-next/cool-next/outbox"

func (a *analysis) analyzeConstructors(root string) []Constructor {
	var constructors []Constructor
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv != nil || function.Name.Name == "ModuleConfig" || strings.HasSuffix(function.Name.Name, "Schema") || !isConstructorName(function.Name.Name) {
					continue
				}
				object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
				signature, _ := object.Type().(*types.Signature)
				if signature == nil || signature.TypeParams().Len() != 0 || signature.Variadic() {
					a.add("CG021", "构造器不能使用类型参数或可变参数", a.position(pkg, function.Pos()))
					continue
				}
				isConsumer := isConsumerSource(root, fileName)
				result, ok := constructorResult(signature)
				if !ok && isConsumer {
					result, ok = consumerResult(signature)
				}
				if !ok {
					a.add("CG022", "构造器必须返回 *T、(*T, error) 或 ConsumerDefinition", a.position(pkg, function.Pos()))
					continue
				}
				isConsumer = isConsumer && isOutboxType(result, "ConsumerDefinition")
				metadata := consumerMetadata{}
				if isConsumer {
					var metadataOK bool
					metadata, metadataOK = a.consumerMetadata(pkg, function)
					if !metadataOK {
						continue
					}
				}
				parameters, values, paramPos, paramDecls := a.signatureParameters(pkg, signature)
				constructors = append(constructors, Constructor{
					consumerMessageType: metadata.messageType,
					consumerName:        metadata.name,
					consumerTopic:       metadata.topic,
					consumerVersions:    metadata.versions,
					hasError:            signature.Results().Len() == 2,
					hasInitializer:      implementsLifecycle(result, "OnInit"),
					isConsumerAdapter:   isConsumerAdapter(result),
					isConsumerDef:       isConsumer,
					isProducer:          needsEnqueuer(values),
					isPublisher:         implementsPublisher(result),
					hasStarter:          implementsLifecycle(result, "OnStart"),
					hasStopper:          implementsLifecycle(result, "OnStop"),
					hasTransport:        implementsTransport(result),
					name:                function.Name.Name,
					packageName:         pkg.packageInfo.Name,
					packagePath:         pkg.packageInfo.PkgPath,
					paramDecls:          paramDecls,
					paramPos:            paramPos,
					parameters:          parameters,
					position:            a.position(pkg, function.Pos()),
					result:              types.TypeString(result, qualifier),
					resultType:          result,
					types:               values,
				})
			}
		}
	}
	sort.Slice(constructors, func(left, right int) bool {
		if constructors[left].packagePath != constructors[right].packagePath {
			return constructors[left].packagePath < constructors[right].packagePath
		}
		if constructors[left].name != constructors[right].name {
			return constructors[left].name < constructors[right].name
		}
		return constructors[left].position.File < constructors[right].position.File
	})
	return constructors
}

func implementsLifecycle(result types.Type, method string) bool {
	selection := types.NewMethodSet(types.Unalias(result)).Lookup(nil, method)
	if selection == nil {
		return false
	}
	signature, ok := selection.Obj().Type().(*types.Signature)
	if !ok || signature.Params().Len() != 1 || signature.Results().Len() != 1 {
		return false
	}
	parameter := types.Unalias(signature.Params().At(0).Type())
	named, namedOK := parameter.(*types.Named)
	return namedOK && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "context" && named.Obj().Name() == "Context" &&
		types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func implementsTransport(result types.Type) bool {
	methodSet := types.NewMethodSet(types.Unalias(result))
	name := methodSet.Lookup(nil, "Name")
	prepare := methodSet.Lookup(nil, "Prepare")
	start := methodSet.Lookup(nil, "Start")
	stop := methodSet.Lookup(nil, "Stop")
	if name == nil || prepare == nil || start == nil || stop == nil {
		return false
	}
	nameSignature, nameOK := name.Obj().Type().(*types.Signature)
	prepareSignature, prepareOK := prepare.Obj().Type().(*types.Signature)
	startSignature, startOK := start.Obj().Type().(*types.Signature)
	stopSignature, stopOK := stop.Obj().Type().(*types.Signature)
	if !nameOK || !prepareOK || !startOK || !stopOK {
		return false
	}
	errorType := types.Universe.Lookup("error").Type()
	return nameSignature.Params().Len() == 0 && nameSignature.Results().Len() == 1 &&
		types.Identical(nameSignature.Results().At(0).Type(), types.Typ[types.String]) &&
		isContextErr(prepareSignature) &&
		startSignature.Params().Len() == 1 && isContextType(startSignature.Params().At(0).Type()) &&
		startSignature.Results().Len() == 2 && isErrorChannel(startSignature.Results().At(0).Type()) &&
		types.Identical(startSignature.Results().At(1).Type(), errorType) &&
		isContextErr(stopSignature)
}

func implementsPublisher(result types.Type) bool {
	publish := types.NewMethodSet(types.Unalias(result)).Lookup(nil, "Publish")
	if publish == nil {
		return false
	}
	signature, ok := publish.Obj().Type().(*types.Signature)
	return ok && signature.Params().Len() == 2 && isContextType(signature.Params().At(0).Type()) &&
		isOutboxType(signature.Params().At(1).Type(), "Envelope") && signature.Results().Len() == 1 &&
		types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func isConsumerAdapter(result types.Type) bool {
	methodSet := types.NewMethodSet(types.Unalias(result))
	name := methodSet.Lookup(nil, "Name")
	capabilities := methodSet.Lookup(nil, "Capabilities")
	prepare := methodSet.Lookup(nil, "Prepare")
	start := methodSet.Lookup(nil, "Start")
	stop := methodSet.Lookup(nil, "Stop")
	if name == nil || capabilities == nil || prepare == nil || start == nil || stop == nil {
		return false
	}
	nameSignature, nameOK := name.Obj().Type().(*types.Signature)
	capSig, capabilitiesOK := capabilities.Obj().Type().(*types.Signature)
	prepareSignature, prepareOK := prepare.Obj().Type().(*types.Signature)
	startSignature, startOK := start.Obj().Type().(*types.Signature)
	stopSignature, stopOK := stop.Obj().Type().(*types.Signature)
	if !nameOK || !capabilitiesOK || !prepareOK || !startOK || !stopOK {
		return false
	}
	errorType := types.Universe.Lookup("error").Type()
	return nameSignature.Params().Len() == 0 && nameSignature.Results().Len() == 1 &&
		types.Identical(nameSignature.Results().At(0).Type(), types.Typ[types.String]) &&
		capSig.Params().Len() == 1 && isContextType(capSig.Params().At(0).Type()) &&
		capSig.Results().Len() == 2 && isOutboxType(capSig.Results().At(0).Type(), "ConsumerCapabilities") &&
		types.Identical(capSig.Results().At(1).Type(), errorType) &&
		isPrepareMethod(prepareSignature) &&
		startSignature.Params().Len() == 1 && isContextType(startSignature.Params().At(0).Type()) &&
		startSignature.Results().Len() == 2 && isErrorChannel(startSignature.Results().At(0).Type()) &&
		types.Identical(startSignature.Results().At(1).Type(), errorType) && isContextErr(stopSignature)
}

func isPrepareMethod(signature *types.Signature) bool {
	if signature.Params().Len() != 3 || !isContextType(signature.Params().At(0).Type()) ||
		!isOutboxSlice(signature.Params().At(1).Type(), "Subscription") ||
		!isOutboxType(signature.Params().At(2).Type(), "DeliverFunc") {
		return false
	}
	return signature.Results().Len() == 1 &&
		types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func isContextErr(signature *types.Signature) bool {
	return signature.Params().Len() == 1 && isContextType(signature.Params().At(0).Type()) &&
		signature.Results().Len() == 1 && types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func isContextType(typ types.Type) bool {
	named, ok := types.Unalias(typ).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "context" && named.Obj().Name() == "Context"
}

func isErrorChannel(typ types.Type) bool {
	channel, ok := types.Unalias(typ).(*types.Chan)
	return ok && channel.Dir() == types.RecvOnly && types.Identical(channel.Elem(), types.Universe.Lookup("error").Type())
}

func isConstructorName(name string) bool {
	return name == "New" || (strings.HasPrefix(name, "New") && len(name) > len("New"))
}

func constructorResult(signature *types.Signature) (types.Type, bool) {
	if signature.Results().Len() != 1 && signature.Results().Len() != 2 {
		return nil, false
	}
	result := signature.Results().At(0).Type()
	pointer, ok := types.Unalias(result).(*types.Pointer)
	if !ok {
		return nil, false
	}
	named, ok := types.Unalias(pointer.Elem()).(*types.Named)
	if !ok || named.Obj().IsAlias() || named.Obj().Pkg() == nil {
		return nil, false
	}
	if signature.Results().Len() == 2 && !types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
		return nil, false
	}
	return result, true
}

func consumerResult(signature *types.Signature) (types.Type, bool) {
	if signature.Results().Len() != 1 && signature.Results().Len() != 2 {
		return nil, false
	}
	result := signature.Results().At(0).Type()
	if !isOutboxType(result, "ConsumerDefinition") {
		return nil, false
	}
	if signature.Results().Len() == 2 && !types.Identical(signature.Results().At(1).Type(), types.Universe.Lookup("error").Type()) {
		return nil, false
	}
	return result, true
}

func isConsumerSource(root, fileName string) bool {
	relative, err := filepath.Rel(root, fileName)
	if err != nil || relative == "." {
		return false
	}
	return strings.Split(filepath.ToSlash(relative), "/")[0] == "consumer"
}

func needsEnqueuer(parameters []types.Type) bool {
	for _, parameter := range parameters {
		if isOutboxType(parameter, "Enqueuer") {
			return true
		}
	}
	return false
}

func isOutboxSlice(typ types.Type, elementName string) bool {
	slice, ok := types.Unalias(typ).(*types.Slice)
	return ok && isOutboxType(slice.Elem(), elementName)
}

func isOutboxType(typ types.Type, name string) bool {
	named, ok := types.Unalias(typ).(*types.Named)
	return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == outboxPackagePath && named.Obj().Name() == name
}

func (a *analysis) signatureParameters(pkg *loadedPackage, signature *types.Signature) ([]string, []types.Type, []Position, []Position) {
	parameters := make([]string, signature.Params().Len())
	values := make([]types.Type, signature.Params().Len())
	positions := make([]Position, signature.Params().Len())
	declarations := make([]Position, signature.Params().Len())
	for index := range signature.Params().Len() {
		parameter := signature.Params().At(index)
		values[index] = parameter.Type()
		parameters[index] = types.TypeString(values[index], qualifier)
		positions[index] = a.position(pkg, parameter.Pos())
		declarations[index] = a.typeDeclPos(values[index])
	}
	return parameters, values, positions, declarations
}

func (a *analysis) typeDeclPos(value types.Type) Position {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj() == nil || named.Obj().Pkg() == nil {
		return Position{}
	}
	pkg := a.packages.byPath[named.Obj().Pkg().Path()]
	if pkg == nil {
		return Position{}
	}
	return a.position(pkg, named.Obj().Pos())
}

func qualifier(current *types.Package) string { return current.Path() }
