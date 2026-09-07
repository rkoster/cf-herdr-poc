package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type token struct {
	kind  byte
	value string
}

func main() {
	print0 := len(os.Args) == 4 && os.Args[1] == "-print0"
	if (!print0 && len(os.Args) != 3) || (print0 && len(os.Args) != 4) {
		fmt.Fprintln(os.Stderr, "usage: checkimports [-print0] ROOT ENTRYPOINT")
		os.Exit(2)
	}
	args := os.Args[1:]
	if print0 {
		args = args[1:]
	}
	files, err := checkImports(args[0], args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "check imports: %v\n", err)
		os.Exit(1)
	}
	if print0 {
		for _, file := range files {
			fmt.Printf("%s%c", file, 0)
		}
	}
}

func checkImports(root, entrypoint string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	entrypoint, err = filepath.Abs(entrypoint)
	if err != nil {
		return nil, err
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
	if err := visit(entrypoint); err != nil {
		return nil, err
	}
	files := make([]string, 0, len(seen))
	for file := range seen {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return nil, err
		}
		files = append(files, filepath.ToSlash(relative))
	}
	sort.Strings(files)
	return files, nil
}

func relativeImports(contents []byte) []string {
	var imports []string
	tokens := lex(contents)
	for index, current := range tokens {
		if current.kind != 'i' {
			continue
		}
		if (current.value == "import" || current.value == "require") && index+2 < len(tokens) && tokens[index+1].value == "(" && tokens[index+2].kind == 's' {
			imports = appendRelative(imports, tokens[index+2].value)
			continue
		}
		if current.value != "import" && current.value != "export" {
			continue
		}
		if index+1 < len(tokens) && tokens[index+1].kind == 's' {
			imports = appendRelative(imports, tokens[index+1].value)
			continue
		}
		for next := index + 1; next+1 < len(tokens) && tokens[next].value != ";"; next++ {
			if tokens[next].value == "from" && tokens[next+1].kind == 's' {
				imports = appendRelative(imports, tokens[next+1].value)
				break
			}
		}
	}
	return imports
}

func appendRelative(imports []string, specifier string) []string {
	if strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") {
		return append(imports, specifier)
	}
	return imports
}

func lex(source []byte) []token {
	var tokens []token
	for index := 0; index < len(source); {
		switch {
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '/':
			index += 2
			for index < len(source) && source[index] != '\n' {
				index++
			}
		case source[index] == '/' && index+1 < len(source) && source[index+1] == '*':
			index += 2
			for index+1 < len(source) && !(source[index] == '*' && source[index+1] == '/') {
				index++
			}
			if index+1 < len(source) {
				index += 2
			}
		case source[index] == '\'' || source[index] == '"':
			quote := source[index]
			index++
			var value strings.Builder
			for index < len(source) && source[index] != quote {
				if source[index] == '\\' && index+1 < len(source) {
					index++
				}
				value.WriteByte(source[index])
				index++
			}
			if index < len(source) {
				index++
			}
			tokens = append(tokens, token{kind: 's', value: value.String()})
		case source[index] == '`':
			index++
			for index < len(source) && source[index] != '`' {
				if source[index] == '\\' && index+1 < len(source) {
					index += 2
				} else {
					index++
				}
			}
			if index < len(source) {
				index++
			}
		case isIdentifierByte(source[index]):
			start := index
			for index < len(source) && isIdentifierByte(source[index]) {
				index++
			}
			tokens = append(tokens, token{kind: 'i', value: string(source[start:index])})
		default:
			if strings.ContainsRune("();,", rune(source[index])) {
				tokens = append(tokens, token{kind: 'p', value: string(source[index])})
			}
			index++
		}
	}
	return tokens
}

func isIdentifierByte(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
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
