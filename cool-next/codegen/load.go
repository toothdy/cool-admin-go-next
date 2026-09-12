package codegen

import (
	"context"
	"go/ast"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type packageIndex struct {
	byFile map[string]*loadedPackage
	byPath map[string]*loadedPackage
}

type loadedPackage struct {
	packageInfo *packages.Package
	syntax      map[string]*ast.File
}

func loadPackages(ctx context.Context, dir, modulesRoot string, overlay map[string][]byte) (*packageIndex, []Diagnostic) {
	relative, err := filepath.Rel(dir, modulesRoot)
	if err != nil {
		return nil, []Diagnostic{{Code: "CG006", Message: "模块根目录不在工作区内"}}
	}
	loaded, err := packages.Load(&packages.Config{
		Context: ctx,
		Dir:     dir,
		Mode:    packages.LoadSyntax | packages.NeedDeps,
		Overlay: cloneOverlay(overlay),
	}, "./"+filepath.ToSlash(relative)+"/...")
	if err != nil {
		return nil, []Diagnostic{{Code: "CG007", Message: "加载 Go Package 失败"}}
	}
	index := &packageIndex{byFile: make(map[string]*loadedPackage), byPath: make(map[string]*loadedPackage)}
	var diagnostics []Diagnostic
	for _, current := range loaded {
		item := &loadedPackage{packageInfo: current, syntax: make(map[string]*ast.File)}
		for fileIndex, file := range current.Syntax {
			if fileIndex < len(current.CompiledGoFiles) {
				item.syntax[current.CompiledGoFiles[fileIndex]] = file
			}
		}
		for _, file := range current.CompiledGoFiles {
			index.byFile[file] = item
		}
		index.byPath[current.PkgPath] = item
		for _, packageError := range current.Errors {
			diagnostics = append(diagnostics, Diagnostic{Code: "CG008", Message: packageError.Msg, Position: packagePosition(dir, packageError.Pos)})
		}
	}
	return index, diagnostics
}

func packagePosition(root, value string) Position {
	file, trailing, ok := splitPositionSuffix(value)
	if !ok {
		return Position{File: value, Line: 1, Column: 1}
	}
	line := trailing
	column := 1
	if path, parsedLine, hasColumn := splitPositionSuffix(file); hasColumn {
		file = path
		line = parsedLine
		column = trailing
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	position := positionFromPath(root, file)
	position.Line = line
	position.Column = column
	return position
}

func splitPositionSuffix(value string) (string, int, bool) {
	separator := strings.LastIndexByte(value, ':')
	if separator < 0 || separator == len(value)-1 {
		return value, 0, false
	}
	number, err := strconv.Atoi(value[separator+1:])
	if err != nil || number < 1 {
		return value, 0, false
	}
	return value[:separator], number, true
}
