package apierrors

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPResponsesDoNotSerializeRawErrors(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate API error package")
	}
	moduleRoot := filepath.Dir(filepath.Dir(sourceFile))
	for _, packageDir := range []string{"controllers", "middlewares", "ratelimit", "router"} {
		files, err := filepath.Glob(filepath.Join(moduleRoot, packageDir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			ast.Inspect(parsed, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isHTTPResponseCall(call) {
					return true
				}
				for _, argument := range call.Args {
					if containsRawErrorValue(argument) {
						t.Errorf("%s: HTTP response call %s may serialize a raw error", file, responseCallName(call))
						break
					}
				}
				return true
			})
		}
	}
}

func TestControllerErrorCodesAreRegistered(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate API error package")
	}
	moduleRoot := filepath.Dir(filepath.Dir(sourceFile))
	files, err := filepath.Glob(filepath.Join(moduleRoot, "controllers", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := responseCallName(call)
			var codeArg ast.Expr
			switch name {
			case "writeAPIError":
				if len(call.Args) == 2 {
					codeArg = call.Args[1]
				}
			case "writeInternalAPIErrorAs":
				if len(call.Args) == 3 {
					codeArg = call.Args[2]
				}
			}
			if codeArg != nil {
				literal, ok := codeArg.(*ast.BasicLit)
				if ok && literal.Kind == token.STRING {
					value, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Errorf("%s: invalid error code literal: %v", file, err)
						return true
					}
					if _, exists := definitions[Code(value)]; !exists {
						t.Errorf("%s: unregistered API error code %q", file, value)
					}
				}
			}
			return true
		})
	}
}

func isHTTPResponseCall(call *ast.CallExpr) bool {
	switch responseCallName(call) {
	case "JSON", "AbortWithStatusJSON", "String", "Data", "DataFromReader", "XML", "YAML", "Error":
		return true
	default:
		return false
	}
}

func responseCallName(call *ast.CallExpr) string {
	switch function := call.Fun.(type) {
	case *ast.SelectorExpr:
		return function.Sel.Name
	case *ast.Ident:
		return function.Name
	default:
		return ""
	}
}

func containsRawErrorValue(expression ast.Node) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if typed.Name == "err" {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if typed.Sel.Name == "Error" {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}
