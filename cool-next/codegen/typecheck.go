package codegen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// 使用候选源码覆盖目标并执行完整类型检查
func checkCandidate(ctx context.Context, options PipelineOptions, candidate []byte) error {
	paths, err := resolvePaths(options)
	if err != nil {
		return err
	}
	loaded, err := packages.Load(&packages.Config{
		Context: ctx,
		Dir:     paths.dir,
		Env:     readOnlyGoEnv(),
		Mode:    packages.LoadAllSyntax,
		Overlay: map[string][]byte{paths.target: append([]byte(nil), candidate...)},
	}, "./"+pipelineModulesRoot)
	if err != nil {
		return pipelineError("CG092", "加载生成候选失败", paths)
	}
	diagnostics := packageDiagnostics(paths, loaded)
	if len(diagnostics) == 0 {
		return nil
	}
	sortDiagnostics(diagnostics)
	return &DiagnosticError{diagnostics: diagnostics}
}

func packageDiagnostics(paths pipelinePaths, roots []*packages.Package) []Diagnostic {
	seen := make(map[string]bool)
	var diagnostics []Diagnostic
	packages.Visit(roots, nil, func(current *packages.Package) {
		hasDetailedError := false
		for _, packageError := range current.Errors {
			if packageError.Kind == packages.ParseError || packageError.Kind == packages.TypeError {
				hasDetailedError = true
				break
			}
		}
		for _, packageError := range current.Errors {
			if hasDetailedError && packageError.Kind == packages.ListError && packageError.Pos == "" {
				continue
			}
			position := stablePosition(paths.dir, current.PkgPath, packageError.Pos)
			message := stableMessage(paths.dir, packageError.Msg)
			key := fmt.Sprintf("%s:%d:%d:%s", position.File, position.Line, position.Column, message)
			if seen[key] {
				continue
			}
			seen[key] = true
			diagnostics = append(diagnostics, Diagnostic{Code: "CG092", Message: message, Position: position})
		}
	})
	return diagnostics
}

func stablePosition(root, packagePath, value string) Position {
	position := packagePosition(root, value)
	if position.File == "" || position.File == "-" || filepath.IsAbs(position.File) || position.File == ".." || strings.HasPrefix(position.File, "../") {
		return Position{File: packagePath, Line: 1, Column: 1}
	}
	return position
}

func stableMessage(root, message string) string {
	cleanedRoot := filepath.Clean(root)
	message = strings.ReplaceAll(message, cleanedRoot, ".")
	return strings.ReplaceAll(message, filepath.ToSlash(cleanedRoot), ".")
}

func readOnlyGoEnv() []string {
	environment := os.Environ()
	goFlags := strings.TrimSpace(os.Getenv("GOFLAGS") + " -mod=readonly")
	for index, variable := range environment {
		if strings.HasPrefix(variable, "GOFLAGS=") {
			environment[index] = "GOFLAGS=" + goFlags
			return environment
		}
	}
	return append(environment, "GOFLAGS="+goFlags)
}
