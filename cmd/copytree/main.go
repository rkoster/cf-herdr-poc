package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: copytree ROOT SOURCE DESTINATION")
		os.Exit(2)
	}
	if err := copyTree(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintf(os.Stderr, "copy tree: %v\n", err)
		os.Exit(1)
	}
}

func copyTree(root, source, destination string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve source: %w", err)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	if !withinRoot(root, source) {
		return fmt.Errorf("source resolves outside root")
	}
	return copyNode(root, source, destination, map[string]bool{})
}

func copyNode(root, source, destination string, active map[string]bool) error {
	name := filepath.Base(source)
	if name == ".git" || name == ".env" || name == ".envrc" {
		return fmt.Errorf("refusing to copy %s", name)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(source)
		if err != nil {
			return fmt.Errorf("resolve symlink %s: %w", source, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return err
		}
		if !withinRoot(root, resolved) {
			return fmt.Errorf("symlink %s resolves outside source root", source)
		}
		return copyNode(root, resolved, destination, active)
	}

	switch {
	case info.IsDir():
		real, err := filepath.EvalSymlinks(source)
		if err != nil {
			return err
		}
		if active[real] {
			return fmt.Errorf("directory cycle at %s", source)
		}
		active[real] = true
		defer delete(active, real)
		if err := os.Mkdir(destination, info.Mode().Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyNode(root, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), active); err != nil {
				return err
			}
		}
		return nil
	case info.Mode().IsRegular():
		return copyFile(source, destination, info.Mode().Perm())
	default:
		return fmt.Errorf("unsupported file type: %s", source)
	}
}

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !(len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator))
}

func copyFile(source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}
