package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var importPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?m)(?:^|[;\n])\s*(?:import|export)\s+(?:[^;"']*?\s+from\s+)?["']([^"']+)["']`),
	regexp.MustCompile(`\bimport\s*\(\s*["']([^"']+)["']\s*\)`),
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: checkimports ROOT ENTRYPOINT")
		os.Exit(2)
	}
	if err := checkImports(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintf(os.Stderr, "check imports: %v\n", err)
		os.Exit(1)
	}
}

func checkImports(root, entrypoint string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	entrypoint, err = filepath.Abs(entrypoint)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var visit func(string) error
	visit = func(source string) error {
		if seen[source] {
			return nil
		}
		seen[source] = true
		contents, err := os.ReadFile(source)
		if err != nil {
			return err
		}
		for _, specifier := range relativeImports(contents) {
			resolved, err := resolveImport(filepath.Dir(source), specifier)
			if err != nil {
				relSource, relErr := filepath.Rel(root, source)
				if relErr != nil {
					relSource = source
				}
				return fmt.Errorf("%s imports missing %s", filepath.ToSlash(relSource), specifier)
			}
			rel, err := filepath.Rel(root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("%s resolves outside runtime root", specifier)
			}
			if err := visit(resolved); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(entrypoint)
}

func relativeImports(contents []byte) []string {
	var imports []string
	for _, pattern := range importPatterns {
		for _, match := range pattern.FindAllSubmatch(contents, -1) {
			specifier := string(match[1])
			if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") {
				imports = append(imports, specifier)
			}
		}
	}
	return imports
}

func resolveImport(dir, specifier string) (string, error) {
	base := filepath.Join(dir, filepath.FromSlash(specifier))
	candidates := []string{base}
	if filepath.Ext(base) == "" {
		for _, extension := range []string{".ts", ".tsx", ".js", ".jsx", ".json"} {
			candidates = append(candidates, base+extension)
		}
		for _, extension := range []string{".ts", ".tsx", ".js", ".jsx"} {
			candidates = append(candidates, filepath.Join(base, "index"+extension))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return filepath.Clean(candidate), nil
		}
	}
	return "", os.ErrNotExist
}
