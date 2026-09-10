package runtime

import (
	"context"
	"errors"
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

func (b Builder) InstallEnrollment(destination, source string) error {
	if err := withinWorkRoot(b.WorkRoot, destination); err != nil {
		return err
	}
	if err := rejectSymlinkComponents(b.WorkRoot, filepath.Join(destination, "sandbox-runtime", "join-token")); err != nil {
		return err
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read enrollment file: %w", err)
	}
	if err := os.WriteFile(filepath.Join(destination, "sandbox-runtime", "join-token"), contents, 0o600); err != nil {
		return fmt.Errorf("install enrollment file: %w", err)
	}
	return nil
}

func (b Builder) Prepare(ctx context.Context, repoURL, destination string) (result Result, resultErr error) {
	if repoURL == "" || strings.HasPrefix(repoURL, "-") {
		return Result{}, fmt.Errorf("invalid repository URL")
	}
	if b.Run == nil {
		return Result{}, fmt.Errorf("command runner is required")
	}
	if err := withinWorkRoot(b.WorkRoot, destination); err != nil {
		return Result{}, err
	}
	_, err := os.Lstat(destination)
	destinationAbsent := errors.Is(err, os.ErrNotExist)
	if err != nil && !destinationAbsent {
		return Result{}, fmt.Errorf("inspect destination: %w", err)
	}
	if destinationAbsent {
		defer func() {
			if resultErr != nil {
				if err := os.RemoveAll(destination); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("remove failed destination: %w", err))
				}
			}
		}()
	}
	if output, err := b.Run.Run(ctx, "git", "clone", "--depth", "1", "--", repoURL, destination); err != nil {
		return Result{}, fmt.Errorf("clone repository: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := withinWorkRoot(b.WorkRoot, destination); err != nil {
		return Result{}, err
	}
	if err := validateRuntime(b.RuntimeDir); err != nil {
		return Result{}, err
	}
	overlay := filepath.Join(destination, "sandbox-runtime")
	if err := validateOverlayDestination(b.RuntimeDir, b.WorkRoot, overlay); err != nil {
		return Result{}, err
	}
	if err := copyRuntime(b.RuntimeDir, overlay); err != nil {
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
	physicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return fmt.Errorf("resolve physical work root: %w", err)
	}
	ancestor, err := nearestExistingAncestor(absDestination)
	if err != nil {
		return err
	}
	physicalAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve physical destination ancestor: %w", err)
	}
	remainder, err := filepath.Rel(ancestor, absDestination)
	if err != nil {
		return fmt.Errorf("resolve destination remainder: %w", err)
	}
	return requireChild(physicalRoot, filepath.Join(physicalAncestor, remainder))
}

func nearestExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		_, err := os.Lstat(current)
		if err == nil {
			return current, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect destination ancestor: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("destination has no existing ancestor")
		}
	}
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
	root, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve runtime directory: %w", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return fmt.Errorf("resolve runtime directory: %w", err)
	}
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if err := validateRuntimeSymlink(root, path); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("validate runtime directory %q: %w", source, err)
	}
	for _, name := range []string{"start.sh", "start-bash.sh", "bin/bun", "bin/herdr", "bin/collie", "bin/opencode", "bin/sandbox-bootstrap"} {
		path := filepath.Join(source, filepath.FromSlash(name))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("runtime asset %q is missing: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("runtime asset %q must be an executable regular file", path)
		}
	}
	return nil
}

func ValidateSandboxRuntime(source string) error {
	return validateRuntime(source)
}

func validateRuntimeSymlink(root, path string) error {
	target, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("read runtime symlink %q: %w", path, err)
	}
	if filepath.IsAbs(target) {
		return fmt.Errorf("runtime symlink %q is absolute", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve runtime symlink %q: %w", path, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("resolve runtime symlink %q: %w", path, err)
	}
	if err := requireChild(root, resolved); err != nil {
		return fmt.Errorf("runtime symlink %q resolves outside runtime: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect runtime symlink %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("runtime symlink %q does not resolve to a regular file", path)
	}
	return nil
}

func validateOverlayDestination(source, workRoot, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if err := rejectSymlinkComponents(workRoot, target); err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect overlay destination %q: %w", target, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("overlay destination contains symlink %q", target)
		}
		if entry.IsDir() && !info.IsDir() {
			return fmt.Errorf("overlay directory path is not a directory %q", target)
		}
		return nil
	})
}

func rejectSymlinkComponents(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve overlay root: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve overlay target: %w", err)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return fmt.Errorf("compare overlay target to work root: %w", err)
	}
	current := absRoot
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect overlay path %q: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("overlay destination contains symlink %q", current)
		}
	}
	return nil
}

func copyRuntime(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			if err := rejectSymlinkComponents(filepath.Dir(destination), filepath.Join(destination, rel)); err != nil {
				return err
			}
			return os.Symlink(target, filepath.Join(destination, rel))
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
