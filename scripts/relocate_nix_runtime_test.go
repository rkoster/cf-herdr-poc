package scripts_test

import (
	"fmt"
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
	interpreter := printPatchelf(t, "--print-interpreter", source)
	link := filepath.Join(temp, "runtime-link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, link, destination, []string{"TARGET_ARCH=" + runtimeArch()})

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
	elf, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(elf) < 4 || string(elf[:4]) != "\x7fELF" {
		t.Fatalf("destination is not a direct ELF executable: %x", elf[:4])
	}
	wantInterpreter := filepath.Join(filepath.Dir(destination), ".runtime-libs", filepath.Base(interpreter))
	if got := printPatchelf(t, "--print-interpreter", destination); got != wantInterpreter {
		t.Fatalf("interpreter = %q, want %q", got, wantInterpreter)
	}
	if got := printPatchelf(t, "--print-rpath", destination); got != "$ORIGIN/.runtime-libs" {
		t.Fatalf("rpath = %q, want $ORIGIN/.runtime-libs", got)
	}
	if _, err := os.Stat(destination + ".real"); !os.IsNotExist(err) {
		t.Fatalf("unexpected payload sibling: %v", err)
	}
}

func TestRelocateNixRuntimeWrapperPreservesArgumentsAndExitStatus(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	source := compileArgvFixture(t, filepath.Join(temp, "nix", "store"))
	destination := filepath.Join(temp, "bundle", "cf")
	output, err := runRelocatorCommandWithArgs(source, destination, []string{"--wrapper", "TARGET_ARCH=" + runtimeArch()})
	if err != nil {
		t.Fatalf("relocate wrapper: %v\n%s", err, output)
	}
	if err := os.RemoveAll(filepath.Join(temp, "nix")); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(destination, "plain", "two words", "", "wild*card")
	got, err := command.CombinedOutput()
	if err != nil || string(got) != "plain\ntwo words\n\nwild*card\n" {
		t.Fatalf("wrapper output=%q err=%v", got, err)
	}
	payload, err := os.Stat(destination + ".real")
	if err != nil {
		t.Fatalf("wrapper payload missing: %v", err)
	}
	if payload.Mode()&0o111 == 0 || string(mustReadFile(t, destination+".real")[:4]) != "\x7fELF" {
		t.Fatalf("wrapper payload is not an executable ELF: mode=%v", payload.Mode())
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(destination), ".cf-libs"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("private CF libraries missing: entries=%d err=%v", len(entries), err)
	}
	loader := filepath.Join(filepath.Dir(destination), ".cf-libs", "ld-linux-x86-64.so.2")
	if runtime.GOARCH == "arm64" {
		loader = filepath.Join(filepath.Dir(destination), ".cf-libs", "ld-linux-aarch64.so.1")
	}
	if info, err := os.Stat(loader); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("bundled loader missing or not executable: info=%v err=%v", info, err)
	}
	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "/nix/store") {
		t.Fatal("wrapper retains a Nix source path")
	}
	if info, err := os.Stat(destination); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("wrapper is not executable: info=%v err=%v", info, err)
	}
	if err := exec.Command(destination, "exit", "7").Run(); err == nil {
		t.Fatal("wrapper did not preserve payload exit status")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 7 {
		t.Fatalf("wrapper exit error = %v, want exit status 7", err)
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
	env := []string{"TARGET_ARCH=" + runtimeArch()}
	runRelocator(t, source, first, env)
	runRelocator(t, source, second, env)

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

func TestRelocateNixRuntimeRejectsArchitectureMismatch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	source := compileArgvFixture(t, filepath.Join(temp, "source"))
	want := "arm64"
	if runtime.GOARCH == "arm64" {
		want = "amd64"
	}
	output, err := runRelocatorCommand(source, filepath.Join(temp, "runtime"), []string{"TARGET_ARCH=" + want})
	if err == nil || !strings.Contains(output, "ELF architecture") {
		t.Fatalf("mismatch output=%q err=%v", output, err)
	}
}

func TestRelocateNixRuntimeRecursivelyCopiesNeededLibraries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	libs := filepath.Join(temp, "libs")
	if err := os.Mkdir(libs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(libs, "inner.c"), "int inner(void) { return 7; }\n")
	compile(t, "cc", "-shared", "-fPIC", "-Wl,-soname,libinner.so", "-o", filepath.Join(libs, "libinner.so"), filepath.Join(libs, "inner.c"))
	writeFile(t, filepath.Join(libs, "outer.c"), "extern int inner(void); int outer(void) { return inner(); }\n")
	compile(t, "cc", "-shared", "-fPIC", "-Wl,-soname,libouter.so", "-Wl,-rpath,$ORIGIN", "-L"+libs, "-o", filepath.Join(libs, "libouter.so"), filepath.Join(libs, "outer.c"), "-linner")
	writeFile(t, filepath.Join(temp, "main.c"), "extern int outer(void); int main(void) { return outer() != 7; }\n")
	source := filepath.Join(temp, "runtime")
	compile(t, "cc", "-Wl,-rpath,"+libs, "-L"+libs, "-o", source, filepath.Join(temp, "main.c"), "-louter")
	tools := filepath.Join(temp, "tools")
	if err := os.Mkdir(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	realLdd, _ := exec.LookPath("ldd")
	writeExecutable(t, filepath.Join(tools, "ldd"), "#!/bin/sh\n\""+realLdd+"\" \"$1\" | grep -v libinner\n")
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, source, destination, []string{"PATH=" + tools + ":" + os.Getenv("PATH"), "TARGET_ARCH=" + runtimeArch()})
	if _, err := os.Stat(filepath.Join(temp, "bundle", ".runtime-libs", "libinner.so")); err != nil {
		t.Fatalf("recursive dependency missing: %v", err)
	}
	if got := printPatchelf(t, "--print-rpath", filepath.Join(temp, "bundle", ".runtime-libs", "libouter.so")); strings.Contains(got, temp) {
		t.Fatalf("copied library retains source RPATH %q", got)
	}
}

func TestRelocateNixRuntimePreservesVersionedSONAMEAlias(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	libs := filepath.Join(temp, "libs")
	if err := os.Mkdir(libs, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(libs, "foo.c"), "int foo(void) { return 42; }\n")
	canonical := filepath.Join(libs, "libfoo.so.1.2")
	compile(t, "cc", "-shared", "-fPIC", "-Wl,-soname,libfoo.so.1", "-o", canonical, filepath.Join(libs, "foo.c"))
	if err := os.Symlink(filepath.Base(canonical), filepath.Join(libs, "libfoo.so.1")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(temp, "main.c"), "extern int foo(void); int main(void) { return foo() != 42; }\n")
	source := filepath.Join(temp, "runtime")
	compile(t, "cc", "-Wl,-rpath,"+libs, "-L"+libs, "-o", source, filepath.Join(temp, "main.c"), "-l:libfoo.so.1")
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, source, destination, []string{"TARGET_ARCH=" + runtimeArch()})

	alias := filepath.Join(temp, "bundle", ".runtime-libs", "libfoo.so.1")
	info, err := os.Lstat(alias)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("SONAME alias is not a symlink: info=%v err=%v", info, err)
	}
	target, err := os.Readlink(alias)
	if err != nil || filepath.IsAbs(target) || filepath.Dir(target) != "." {
		t.Fatalf("SONAME alias target = %q, err=%v; want same-directory relative target", target, err)
	}
	resolved, err := filepath.EvalSymlinks(alias)
	if err != nil || filepath.Dir(resolved) != filepath.Dir(alias) {
		t.Fatalf("SONAME alias escapes private directory: resolved=%q err=%v", resolved, err)
	}
	if output, err := exec.Command(destination).CombinedOutput(); err != nil {
		t.Fatalf("relocated versioned fixture failed: %v\n%s", err, output)
	}
}

func TestRelocateNixRuntimeRejectsDifferentContentForSameSONAME(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	for _, fixture := range []struct {
		name  string
		value int
	}{
		{name: "a", value: 1},
		{name: "b", value: 2},
	} {
		dir := filepath.Join(temp, fixture.name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "foo.c"), fmt.Sprintf("int foo(void) { return %d; }\n", fixture.value))
		compile(t, "cc", "-shared", "-fPIC", "-Wl,-soname,libfoo.so.1", "-o", filepath.Join(dir, "libfoo.so.1.2"), filepath.Join(dir, "foo.c"))
		if err := os.Symlink("libfoo.so.1.2", filepath.Join(dir, "libfoo.so.1")); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, fixture.name+".c"), "extern int foo(void); int "+fixture.name+"(void) { return foo(); }\n")
		compile(t, "cc", "-shared", "-fPIC", "-Wl,-soname,lib"+fixture.name+".so", "-Wl,-rpath,$ORIGIN", "-L"+dir, "-o", filepath.Join(dir, "lib"+fixture.name+".so"), filepath.Join(dir, fixture.name+".c"), "-l:libfoo.so.1")
	}
	writeFile(t, filepath.Join(temp, "main.c"), "extern int a(void); extern int b(void); int main(void) { return a() + b() != 3; }\n")
	source := filepath.Join(temp, "runtime")
	compile(t, "cc", "-Wl,-rpath,"+filepath.Join(temp, "a")+":"+filepath.Join(temp, "b"), "-L"+filepath.Join(temp, "a"), "-L"+filepath.Join(temp, "b"), "-o", source, filepath.Join(temp, "main.c"), "-la", "-lb")
	output, err := runRelocatorCommand(source, filepath.Join(temp, "bundle", "runtime"), []string{"TARGET_INTERPRETER=" + printPatchelf(t, "--print-interpreter", source), "TARGET_ARCH=" + runtimeArch()})
	if err == nil || !strings.Contains(output, "SONAME collision: libfoo.so.1") {
		t.Fatalf("collision output=%q err=%v", output, err)
	}
}

func TestRelocatedBunPreservesExecPathAndSelfSpawn(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip(err)
	}
	bun, err = filepath.EvalSymlinks(bun)
	if err != nil || !strings.HasPrefix(bun, "/nix/store/") {
		t.Skip("Nix Bun unavailable")
	}
	targetDir := t.TempDir()
	destination := filepath.Join(targetDir, "bun")
	runRelocator(t, bun, destination, []string{"TARGET_INSTALL_DIR=" + targetDir, "TARGET_ARCH=" + runtimeArch()})
	program := `if (process.execPath !== process.argv[0]) throw new Error(process.execPath); const p=Bun.spawnSync([process.execPath,"--version"]); if (p.exitCode !== 0 || !p.stdout.toString().includes(Bun.version)) throw new Error(p.stdout.toString()); console.log(process.execPath)`
	output, err := exec.Command(destination, "-e", program).CombinedOutput()
	if err != nil {
		t.Fatalf("relocated Bun self-spawn: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != destination {
		t.Fatalf("process.execPath = %q, want %q", strings.TrimSpace(string(output)), destination)
	}
}

func TestRelocateNixRuntimeRejectsInvalidTargetInstallDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	source := compileArgvFixture(t, t.TempDir())
	for _, target := range []string{"", "relative/bin", "/home/vcap/app/../escape", "/home/vcap//app/bin", "/home/vcap/app/with space"} {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			output, err := runRelocatorCommandRaw(source, filepath.Join(t.TempDir(), "runtime"), []string{"TARGET_INSTALL_DIR=" + target, "TARGET_ARCH=" + runtimeArch()})
			if err == nil || !strings.Contains(output, "TARGET_INSTALL_DIR") {
				t.Fatalf("target %q: output=%q err=%v", target, output, err)
			}
		})
	}
}

func TestRelocatedRuntimeRunsThroughBundledLoaderSmokeHelper(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ELF relocation is Linux-only")
	}
	for _, tool := range []string{"cc", "ldd", "patchelf"} {
		requireHostTool(t, tool)
	}
	temp := t.TempDir()
	source := compileArgvFixture(t, filepath.Join(temp, "source"))
	destination := filepath.Join(temp, "bundle", "runtime")
	runRelocator(t, source, destination, []string{"TARGET_ARCH=" + runtimeArch()})
	_, filename, _, _ := runtime.Caller(0)
	command := exec.Command("bash", filepath.Join(filepath.Dir(filename), "smoke-relocated-runtime.sh"), destination, "smoke")
	command.Env = append(os.Environ(), "TARGET_ARCH="+runtimeArch())
	output, err := command.CombinedOutput()
	if err != nil || string(output) != "smoke\n" {
		t.Fatalf("bundled-loader smoke: output=%q err=%v", output, err)
	}
}

func TestRelocatorIndexesClosuresOfResolvedNixLibraries(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "relocate-nix-runtime.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `add_nix_closure "$path"`) {
		t.Fatal("relocator does not index closures referenced by ldd")
	}
}

func compileArgvFixture(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "argv.c")
	program := "#include <stdio.h>\n#include <string.h>\nint main(int argc, char **argv) { for (int i = 1; i < argc; i++) printf(\"%s\\n\", argv[i]); return argc > 1 && strcmp(argv[1], \"exit\") == 0 ? 7 : 0; }\n"
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "runtime")
	if output, err := exec.Command("cc", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("compile fixture: %v\n%s", err, output)
	}
	return binary
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func compile(t *testing.T, name string, args ...string) {
	t.Helper()
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}

func printPatchelf(t *testing.T, option, path string) string {
	t.Helper()
	output, err := exec.Command("patchelf", option, path).CombinedOutput()
	if err != nil {
		t.Fatalf("patchelf %s: %v\n%s", option, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runtimeArch() string {
	if runtime.GOARCH == "arm64" {
		return "arm64"
	}
	return "amd64"
}

func runRelocator(t *testing.T, source, destination string, env []string) {
	t.Helper()
	hasTarget := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "TARGET_INSTALL_DIR=") {
			hasTarget = true
		}
	}
	if !hasTarget {
		env = append(env, "TARGET_INSTALL_DIR="+filepath.Dir(destination))
	}
	if output, err := runRelocatorCommand(source, destination, env); err != nil {
		t.Fatalf("relocate runtime: %v\n%s", err, output)
	}
}

func runRelocatorCommand(source, destination string, env []string) (string, error) {
	return runRelocatorCommandWithArgs(source, destination, append([]string{}, env...))
}

func runRelocatorCommandWithArgs(source, destination string, args []string) (string, error) {
	hasTarget := false
	for _, entry := range args {
		if strings.HasPrefix(entry, "TARGET_INSTALL_DIR=") {
			hasTarget = true
		}
	}
	if !hasTarget {
		args = append(args, "TARGET_INSTALL_DIR="+filepath.Dir(destination))
	}
	_, filename, _, _ := runtime.Caller(0)
	commandArgs := []string{}
	env := os.Environ()
	for _, arg := range args {
		if strings.Contains(arg, "=") && !strings.HasPrefix(arg, "--") {
			env = append(env, arg)
		} else {
			commandArgs = append(commandArgs, arg)
		}
	}
	commandArgs = append(commandArgs, source, destination)
	command := exec.Command("bash", append([]string{filepath.Join(filepath.Dir(filename), "relocate-nix-runtime.sh")}, commandArgs...)...)
	command.Env = env
	output, err := command.CombinedOutput()
	return string(output), err
}

func runRelocatorCommandRaw(source, destination string, env []string) (string, error) {
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
