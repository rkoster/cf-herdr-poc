package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckImportsReportsMissingTransitiveRelativeImport(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "entry.ts"), `export { value } from "./nested.ts";`)
	writeSource(t, filepath.Join(root, "nested.ts"), `const load = () => import("./missing.ts"); export const value = load;`)

	output, err := runCheckImports(t, root, filepath.Join(root, "entry.ts"))
	if err == nil {
		t.Fatal("checkimports accepted a missing transitive relative import")
	}
	if !strings.Contains(output, "nested.ts imports missing ./missing.ts") {
		t.Fatalf("output = %q, want missing transitive import", output)
	}
}

func TestCheckImportsAcceptsResolvedRelativeImportClosure(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "entry.ts"), `import { value } from "./nested"; export default value;`)
	writeSource(t, filepath.Join(root, "nested.ts"), `export const value = 1;`)

	output, err := runCheckImports(t, root, filepath.Join(root, "entry.ts"))
	if err != nil {
		t.Fatalf("checkimports failed: %v\n%s", err, output)
	}
}

func TestCheckImportsRecognizesRuntimeModuleForms(t *testing.T) {
	for name, source := range map[string]string{
		"require":         `const dependency = require("./missing.ts");`,
		"dynamic-options": `const dependency = import("./missing.json", { with: { type: "json" } });`,
		"bare-import":     `import "./missing.ts";`,
		"static-import":   `import { value } from "./missing.ts";`,
		"static-export":   `export { value } from "./missing.ts";`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSource(t, filepath.Join(root, "entry.ts"), source)
			output, err := runCheckImports(t, root, filepath.Join(root, "entry.ts"))
			if err == nil || !strings.Contains(output, "imports missing ./missing") {
				t.Fatalf("output=%q err=%v, want missing import", output, err)
			}
		})
	}
}

func TestCheckImportsIgnoresModuleSyntaxInCommentsAndStrings(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "entry.ts"), `
// require("./comment.ts")
/* import "./block-comment.ts"; */
const examples = "import('./string.ts') and export from './also-string.ts'";
`+"const template = `require(\"./template.ts\")`;\n"+`
export { examples };
`)
	output, err := runCheckImports(t, root, filepath.Join(root, "entry.ts"))
	if err != nil {
		t.Fatalf("checkimports failed: %v\n%s", err, output)
	}
}

func TestCheckImportsPrintsNullDelimitedReachableFiles(t *testing.T) {
	root := t.TempDir()
	writeSource(t, filepath.Join(root, "entry.ts"), `import "./nested.ts";`)
	writeSource(t, filepath.Join(root, "nested.ts"), `export const value = 1;`)

	command := exec.Command("go", "run", "./cmd/checkimports", "-print0", root, filepath.Join(root, "entry.ts"))
	command.Dir = packageRoot(t)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "entry.ts\x00nested.ts\x00"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func runCheckImports(t *testing.T, root, entry string) (string, error) {
	t.Helper()
	command := exec.Command("go", "run", "./cmd/checkimports", root, entry)
	command.Dir = packageRoot(t)
	output, err := command.CombinedOutput()
	return string(output), err
}

func writeSource(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
