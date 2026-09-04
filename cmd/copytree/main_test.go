package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTreeMaterializesInternalSymlink(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	if err := os.Mkdir(filepath.Join(source, "package"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "package", "cli.js"), []byte("safe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("package/cli.js", filepath.Join(source, "cli")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(source, source, destination); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(destination, "cli"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("internal symlink was not materialized")
	}
	contents, err := os.ReadFile(filepath.Join(destination, "cli"))
	if err != nil || string(contents) != "safe" {
		t.Fatalf("contents=%q err=%v", contents, err)
	}
}

func TestCopyTreeRejectsExternalAndBrokenSymlinks(t *testing.T) {
	for _, target := range []string{"../outside", "missing"} {
		t.Run(target, func(t *testing.T) {
			parent := t.TempDir()
			source := filepath.Join(parent, "source")
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			if target == "../outside" {
				if err := os.WriteFile(filepath.Join(parent, "outside"), []byte("secret"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(source, "link")); err != nil {
				t.Fatal(err)
			}
			if err := copyTree(source, source, filepath.Join(parent, "copy")); err == nil {
				t.Fatalf("accepted unsafe symlink %q", target)
			}
		})
	}
}

func TestCopyTreeAllowsSelectedTreeLinkWithinDeclaredRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "selected")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(root, source, destination); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(destination, "link"))
	if err != nil || string(contents) != "safe" {
		t.Fatalf("contents=%q err=%v", contents, err)
	}
}

func TestCopyTreeRejectsSourceOutsideRootAndSecretControlNames(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(root, outside, filepath.Join(t.TempDir(), "copy")); err == nil {
		t.Fatal("accepted source outside root")
	}
	for _, name := range []string{".git", ".env", ".envrc"} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(root, "selected-"+strings.TrimPrefix(name, "."))
			if err := os.Mkdir(source, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, name), []byte("secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := copyTree(root, source, filepath.Join(t.TempDir(), "copy")); err == nil {
				t.Fatalf("accepted forbidden %s", name)
			}
		})
	}
}
