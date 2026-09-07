package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRelocateNixRuntimeExecutesWithExactArguments(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	requireHostTool(t, "cc")
	requireHostTool(t, "ldd")
	requireHostTool(t, "patchelf")

	temp := t.TempDir()
	sourceDir := filepath.Join(temp, "nix", "store", "fixture-runtime")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := compileArgvFixture(t, sourceDir)
	link := filepath.Join(temp, "runtime-link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, link, destination, nil)

	if err := os.RemoveAll(filepath.Join(temp, "nix")); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(destination, "plain", "two words", "", "wild*card").CombinedOutput()
	if err != nil {
		t.Fatalf("relocated runtime failed: %v\n%s", err, output)
	}
	if got, want := string(output), "plain\ntwo words\n\nwild*card\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	wrapper, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wrapper), sourceDir) || strings.Contains(string(wrapper), "/nix/store/") {
		t.Fatalf("wrapper embeds source path: %s", wrapper)
	}
	for _, path := range []string{destination + ".real", filepath.Join(filepath.Dir(destination), ".runtime-libs")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing relocated artifact %s: %v", path, err)
		}
	}
}

func TestRelocateNixRuntimeUsesIndependentLibraryDirectories(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	source := compileArgvFixture(t, filepath.Join(temp, "nix-like"))
	first := filepath.Join(temp, "bundle", "bun")
	second := filepath.Join(temp, "bundle", "herdr")
	runRelocator(t, source, first, nil)
	runRelocator(t, source, second, nil)

	for _, dir := range []string{".bun-libs", ".herdr-libs"} {
		entries, err := os.ReadDir(filepath.Join(temp, "bundle", dir))
		if err != nil || len(entries) == 0 {
			t.Fatalf("private library directory %s: entries=%d err=%v", dir, len(entries), err)
		}
	}
}

func TestRelocateNixRuntimeRejectsMalformedLddOutput(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "patchelf"} {
		requireHostTool(t, tool)
	}
	for _, test := range []struct {
		name  string
		line  string
		error string
	}{
		{name: "relative dependency", line: "libbroken.so => relative/path (0x1234)", error: "malformed ldd output"},
		{name: "unresolved dependency", line: "libmissing.so => not found", error: "unresolved dependency"},
		{name: "arbitrary output", line: "surprising output", error: "malformed ldd output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			temp := t.TempDir()
			source := compileArgvFixture(t, filepath.Join(temp, "source"))
			tools := filepath.Join(temp, "tools")
			if err := os.Mkdir(tools, 0o755); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(tools, "ldd"), "#!/bin/sh\nprintf '%s\\n' '"+test.line+"'\n")
			output, err := runRelocatorCommand(source, filepath.Join(temp, "runtime"), []string{"PATH=" + tools + ":" + os.Getenv("PATH")})
			if err == nil {
				t.Fatalf("relocator accepted malformed ldd output: %s", output)
			}
			if !strings.Contains(output, test.error) {
				t.Fatalf("output = %q, want %q", output, test.error)
			}
		})
	}
}

func TestRelocateNixRuntimeUsesPTInterpInsteadOfLddLoaderMapping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	source := compileArgvFixture(t, filepath.Join(temp, "source"))
	realLdd, _ := exec.LookPath("ldd")
	interpreterOutput, err := exec.Command("patchelf", "--print-interpreter", source).Output()
	if err != nil {
		t.Fatal(err)
	}
	interpreter := strings.TrimSpace(string(interpreterOutput))
	tools := filepath.Join(temp, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	otherLoader := filepath.Join(temp, filepath.Base(interpreter))
	if err := os.WriteFile(otherLoader, []byte("different loader"), 0o755); err != nil {
		t.Fatal(err)
	}
	lddScript := "#!/bin/sh\n\"" + realLdd + "\" \"$1\"\nprintf '%s => %s (0x1234)\\n' '" + interpreter + "' '" + otherLoader + "'\n"
	writeExecutable(t, filepath.Join(tools, "ldd"), lddScript)
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, source, destination, []string{"PATH=" + tools + ":" + os.Getenv("PATH")})
	if output, err := exec.Command(destination, "works").CombinedOutput(); err != nil || string(output) != "works\n" {
		t.Fatalf("relocated runtime: output=%q err=%v", output, err)
	}
}

func compileArgvFixture(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "argv.c")
	program := "#include <stdio.h>\nint main(int argc, char **argv) { for (int i = 1; i < argc; i++) printf(\"%s\\n\", argv[i]); return 0; }\n"
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runtime")
	if output, err := exec.Command("cc", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v\n%s", err, output)
	}
	return binary
}

func runRelocator(t *testing.T, source, destination string, env []string) {
	t.Helper()
	if output, err := runRelocatorCommand(source, destination, env); err != nil {
		t.Fatalf("relocate runtime: %v\n%s", err, output)
	}
}

func runRelocatorCommand(source, destination string, env []string) (string, error) {
	_, filename, _, _ := runtime.Caller(0)
	command := exec.Command("bash", filepath.Join(filepath.Dir(filename), "relocate-nix-runtime.sh"), source, destination)
	command.Env = append(os.Environ(), env...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func requireHostTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is unavailable: %v", name, err)
	}
}
