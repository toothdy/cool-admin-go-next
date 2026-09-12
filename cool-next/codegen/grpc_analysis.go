package codegen

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
)

const grpcxPackagePath = "github.com/gogf/gf/contrib/rpc/grpcx/v2"
const protoreflectPath = "google.golang.org/protobuf/reflect/protoreflect"

func (a *analysis) analyzeGRPC(root string) []GRPCRegistrarDeclaration {
	var result []GRPCRegistrarDeclaration
	for _, pkg := range a.modulePackages(root) {
		for fileName, file := range pkg.syntax {
			if !a.eligible[fileName] {
				continue
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				object, _ := pkg.packageInfo.TypesInfo.Defs[function.Name].(*types.Func)
				if object == nil {
					continue
				}
				signature, _ := object.Type().(*types.Signature)
				if signature != nil && isServiceFile(root, fileName) && hasProtoParam(signature) {
					a.add("CG106", "业务 Service 方法不能接收 protobuf Message", a.position(pkg, function.Pos()))
				}
				if function.Name.Name != "Register" {
					continue
				}
				if signature == nil || !isGRPCFile(root, fileName) && !mentionsGRPCServer(signature) {
					continue
				}
				if !validGRPCRegistrar(function, signature) {
					a.add("CG105", "gRPC Register 必须声明为 func(*grpcx.GrpcServer) error", a.position(pkg, function.Pos()))
					continue
				}
				result = append(result, GRPCRegistrarDeclaration{
					name:        function.Name.Name,
					packageName: pkg.packageInfo.Name,
					packagePath: pkg.packageInfo.PkgPath,
					position:    a.position(pkg, function.Pos()),
				})
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].packagePath != result[right].packagePath {
			return result[left].packagePath < result[right].packagePath
		}

		return result[left].position.File < result[right].position.File
	})

	return result
}

func isGRPCFile(root, fileName string) bool {
	return moduleSubdirectory(root, fileName) == "grpc"
}

func isServiceFile(root, fileName string) bool {
	return moduleSubdirectory(root, fileName) == "service"
}

func moduleSubdirectory(root, fileName string) string {
	relative, err := filepath.Rel(root, fileName)
	if err != nil {
		return ""
	}

	return strings.Split(filepath.ToSlash(relative), "/")[0]
}

func mentionsGRPCServer(signature *types.Signature) bool {
	for index := range signature.Params().Len() {
		if isGRPCServer(signature.Params().At(index).Type()) {
			return true
		}
	}

	return false
}

func validGRPCRegistrar(function *ast.FuncDecl, signature *types.Signature) bool {
	return function.Recv == nil && signature.TypeParams().Len() == 0 && !signature.Variadic() &&
		signature.Params().Len() == 1 && isGRPCServerPointer(signature.Params().At(0).Type()) &&
		signature.Results().Len() == 1 && types.Identical(signature.Results().At(0).Type(), types.Universe.Lookup("error").Type())
}

func isGRPCServer(value types.Type) bool {
	value = types.Unalias(value)
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	named, ok := value.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == grpcxPackagePath && named.Obj().Name() == "GrpcServer"
}

func isGRPCServerPointer(value types.Type) bool {
	pointer, ok := types.Unalias(value).(*types.Pointer)
	return ok && isGRPCServer(pointer.Elem())
}

func hasProtoParam(signature *types.Signature) bool {
	for index := range signature.Params().Len() {
		if hasProtobuf(signature.Params().At(index).Type()) {
			return true
		}
	}

	return false
}

func hasProtobuf(value types.Type) bool {
	value = types.Unalias(value)
	if isProtobufMessage(value) {
		return true
	}
	switch current := value.(type) {
	case *types.Array:
		return hasProtobuf(current.Elem())
	case *types.Chan:
		return hasProtobuf(current.Elem())
	case *types.Map:
		return hasProtobuf(current.Key()) || hasProtobuf(current.Elem())
	case *types.Slice:
		return hasProtobuf(current.Elem())
	}

	return false
}

func isProtobufMessage(value types.Type) bool {
	method := types.NewMethodSet(value).Lookup(nil, "ProtoReflect")
	if method == nil {
		return false
	}
	signature, ok := method.Obj().Type().(*types.Signature)
	if !ok || signature.Params().Len() != 0 || signature.Results().Len() != 1 {
		return false
	}
	result, ok := types.Unalias(signature.Results().At(0).Type()).(*types.Named)
	return ok && result.Obj().Pkg() != nil && result.Obj().Pkg().Path() == protoreflectPath && result.Obj().Name() == "Message"
}
