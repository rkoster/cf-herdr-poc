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
