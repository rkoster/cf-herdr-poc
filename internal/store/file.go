package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"

	"cf-herdr-poc/internal/model"
)

type File struct {
	mu            sync.Mutex
	path          string
	sandboxes     map[string]model.Sandbox
	syncDirectory func(string) error
}

func NewFile(path string) *File {
	return &File{
		path:          path,
		sandboxes:     make(map[string]model.Sandbox),
		syncDirectory: syncDirectory,
	}
}

func (f *File) Load() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	file, err := os.Open(f.path)
	if errors.Is(err, os.ErrNotExist) {
		f.sandboxes = make(map[string]model.Sandbox)
		return nil
	}
	if err != nil {
		return fmt.Errorf("open sandbox state: %w", err)
	}
	defer file.Close()

	var sandboxes []model.Sandbox
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&sandboxes); err != nil {
		return fmt.Errorf("decode sandbox state: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return fmt.Errorf("decode sandbox state: %w", err)
	}

	loaded := make(map[string]model.Sandbox, len(sandboxes))
	for _, sandbox := range sandboxes {
		if _, exists := loaded[sandbox.Name]; exists {
			return fmt.Errorf("decode sandbox state: duplicate sandbox %q", sandbox.Name)
		}
		loaded[sandbox.Name] = cloneSandbox(sandbox)
	}
	f.sandboxes = loaded
	return nil
}

func (f *File) List() []model.Sandbox {
	f.mu.Lock()
	defer f.mu.Unlock()

	result := make([]model.Sandbox, 0, len(f.sandboxes))
	for _, sandbox := range f.sandboxes {
		result = append(result, cloneSandbox(sandbox))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].Name < result[j].Name
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

func (f *File) Get(name string) (model.Sandbox, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sandbox, ok := f.sandboxes[name]
	return cloneSandbox(sandbox), ok
}

func (f *File) Create(sandbox model.Sandbox) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sandboxes[sandbox.Name]; exists {
		return fmt.Errorf("sandbox %q already exists", sandbox.Name)
	}
	next := cloneSandboxes(f.sandboxes)
	next[sandbox.Name] = cloneSandbox(sandbox)
	return f.commit(next)
}

func (f *File) Update(name string, update func(*model.Sandbox) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	sandbox, exists := f.sandboxes[name]
	if !exists {
		return fmt.Errorf("sandbox %q does not exist", name)
	}
	next := cloneSandboxes(f.sandboxes)
	sandbox = cloneSandbox(sandbox)
	if err := update(&sandbox); err != nil {
		return err
	}
	if sandbox.Name != name {
		return errors.New("sandbox update cannot change name")
	}
	next[name] = cloneSandbox(sandbox)
	return f.commit(next)
}

func (f *File) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sandboxes[name]; !exists {
		return fmt.Errorf("sandbox %q does not exist", name)
	}
	next := cloneSandboxes(f.sandboxes)
	delete(next, name)
	return f.commit(next)
}

func (f *File) commit(next map[string]model.Sandbox) error {
	sandboxes := make([]model.Sandbox, 0, len(next))
	for _, sandbox := range next {
		sandboxes = append(sandboxes, sandbox)
	}
	sort.Slice(sandboxes, func(i, j int) bool {
		if sandboxes[i].CreatedAt.Equal(sandboxes[j].CreatedAt) {
			return sandboxes[i].Name < sandboxes[j].Name
		}
		return sandboxes[i].CreatedAt.Before(sandboxes[j].CreatedAt)
	})
	renamed, err := writeAtomic(f.path, sandboxes, f.syncDirectory)
	if renamed {
		// Rename made this snapshot authoritative even if directory durability
		// could not be confirmed.
		f.sandboxes = next
	}
	if err != nil {
		return err
	}
	if !renamed {
		return errors.New("sandbox state was not replaced")
	}
	return nil
}

func writeAtomic(path string, sandboxes []model.Sandbox, syncDir func(string) error) (renamed bool, err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, fmt.Errorf("create sandbox state directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return false, fmt.Errorf("create temporary sandbox state: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		_ = os.Remove(tempName)
	}()

	if err := temp.Chmod(0o600); err != nil {
		return false, fmt.Errorf("set temporary sandbox state permissions: %w", err)
	}
	encoder := json.NewEncoder(temp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sandboxes); err != nil {
		return false, fmt.Errorf("encode sandbox state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return false, fmt.Errorf("sync temporary sandbox state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("close temporary sandbox state: %w", err)
	}
	temp = nil
	if err := os.Rename(tempName, path); err != nil {
		return false, fmt.Errorf("replace sandbox state: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return true, fmt.Errorf("sync sandbox state directory: %w", err)
	}
	return true, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	if errors.Is(syncErr, syscall.EINVAL) || errors.Is(syncErr, syscall.ENOTSUP) || errors.Is(syncErr, syscall.EBADF) {
		// Some platforms and filesystems do not support syncing directories.
		syncErr = nil
	}
	return errors.Join(syncErr, directory.Close())
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}

func cloneSandboxes(source map[string]model.Sandbox) map[string]model.Sandbox {
	result := make(map[string]model.Sandbox, len(source))
	for name, sandbox := range source {
		result[name] = cloneSandbox(sandbox)
	}
	return result
}

func cloneSandbox(sandbox model.Sandbox) model.Sandbox {
	sandbox.Operations = append([]model.Operation(nil), sandbox.Operations...)
	return sandbox
}
