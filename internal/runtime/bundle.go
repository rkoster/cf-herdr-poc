package runtime

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"cf-herdr-poc/internal/runner"
)

type Result struct {
	Revision string
}

type Builder struct {
	Run        runner.Runner
	RuntimeDir string
	WorkRoot   string
}

func (b Builder) Prepare(ctx context.Context, repoURL, destination string) (Result, error) {
	if repoURL == "" || strings.HasPrefix(repoURL, "-") {
		return Result{}, fmt.Errorf("invalid repository URL")
	}
	if b.Run == nil {
		return Result{}, fmt.Errorf("command runner is required")
	}
	if err := withinWorkRoot(b.WorkRoot, destination); err != nil {
		return Result{}, err
	}
	if output, err := b.Run.Run(ctx, "git", "clone", "--depth", "1", "--", repoURL, destination); err != nil {
		return Result{}, fmt.Errorf("clone repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := validateRuntime(b.RuntimeDir); err != nil {
		return Result{}, err
	}
	if err := copyRuntime(b.RuntimeDir, filepath.Join(destination, ".sandbox")); err != nil {
		return Result{}, fmt.Errorf("overlay runtime: %w", err)
	}
	output, err := b.Run.Run(ctx, "git", "-C", destination, "rev-parse", "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("resolve revision: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return Result{Revision: strings.TrimSpace(string(output))}, nil
}

func withinWorkRoot(workRoot, destination string) error {
	if workRoot == "" || destination == "" {
		return fmt.Errorf("work root and destination are required")
	}
	if err := requireChild(workRoot, destination); err != nil {
		return err
	}
	absRoot, err := filepath.Abs(workRoot)
	if err != nil {
		return fmt.Errorf("resolve work root: %w", err)
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	return requireChild(absRoot, absDestination)
}

func requireChild(root, destination string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(destination))
	if err != nil {
		return fmt.Errorf("compare destination to work root: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("destination must be a child of work root")
	}
	return nil
}

func validateRuntime(source string) error {
	if source == "" {
		return fmt.Errorf("runtime directory is required")
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime contains symlink %q", path)
		}
		return nil
	})
}

func copyRuntime(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("runtime contains symlink %q", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
