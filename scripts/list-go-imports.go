// Command list-go-imports reports imports from every Go source file without
// applying build constraints. The architecture checker uses it to cover all
// supported targets and explicitly tagged source in one deterministic pass.
package main

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type packageImports struct {
	PackagePath string   `json:"packagePath"`
	Imports     []string `json:"imports"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/list-go-imports.go <repository-root>")
		os.Exit(2)
	}

	root, err := filepath.Abs(os.Args[1])
	if err != nil {
		fail(err)
	}
	packages := make(map[string]map[string]struct{})
	for _, directory := range []string{"cmd", "internal"} {
		if err := collectImports(root, filepath.Join(root, directory), packages); err != nil {
			fail(err)
		}
	}

	result := make([]packageImports, 0, len(packages))
	for packagePath, imports := range packages {
		values := make([]string, 0, len(imports))
		for importedPackage := range imports {
			values = append(values, importedPackage)
		}
		sort.Strings(values)
		result = append(result, packageImports{PackagePath: packagePath, Imports: values})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PackagePath < result[j].PackagePath })
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fail(err)
	}
}

func collectImports(root, directory string, packages map[string]map[string]struct{}) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			if err := collectImports(root, path, packages); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		packagePath, err := filepath.Rel(root, directory)
		if err != nil {
			return err
		}
		packagePath = filepath.ToSlash(packagePath)
		if packages[packagePath] == nil {
			packages[packagePath] = make(map[string]struct{})
		}
		for _, imported := range parsed.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %s: %w", path, err)
			}
			packages[packagePath][value] = struct{}{}
		}
	}
	return nil
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "list Go imports: %v\n", err)
	os.Exit(1)
}
