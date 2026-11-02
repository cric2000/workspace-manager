package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishWritesExactFilesWithoutOverwritingAnyDestination(t *testing.T) {
	root := t.TempDir()
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	plan, _ := NewRegistry().Render("go", Options{Name: "hello", ModulePath: "example.com/hello"})
	result, err := parent.Publish(context.Background(), "hello", plan.Files)
	if err != nil || !result.Published || !result.Durable || result.RecoveryPath != "" {
		t.Fatal(result, err)
	}
	for _, file := range plan.Files {
		data, err := os.ReadFile(filepath.Join(root, "hello", file.Path))
		if err != nil || string(data) != file.Contents {
			t.Fatal(file.Path, err)
		}
		info, _ := os.Stat(filepath.Join(root, "hello", file.Path))
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	if got, err := parent.Publish(context.Background(), "hello", plan.Files); !errors.Is(err, ErrExists) || got.Published {
		t.Fatal(got, err)
	}
	os.Mkdir(filepath.Join(root, "empty"), 0700)
	os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "link"))
	os.WriteFile(filepath.Join(root, "file"), []byte("keep"), 0600)
	for _, name := range []string{"empty", "link", "file"} {
		if got, err := parent.Publish(context.Background(), name, plan.Files); !errors.Is(err, ErrExists) || got.Published {
			t.Fatal(name, got, err)
		}
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 4 {
		t.Fatal("staging leftovers", entries)
	}
}

// Triggers genuine cancellation at a deterministic cooperative checkpoint.
type cancelAtCheck struct {
	context.Context
	cancel       context.CancelFunc
	checks       int
	beforeCancel func()
}

func (ctx *cancelAtCheck) Err() error {
	ctx.checks--
	if ctx.checks == 0 {
		if ctx.beforeCancel != nil {
			ctx.beforeCancel()
		}
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestCancellationCleansOnlyTheStagedOperation(t *testing.T) {
	root := t.TempDir()
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, checkpoint := range []int{1, 2, 3, 4, 5} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtCheck{Context: base, cancel: cancel, checks: checkpoint}
		got, err := parent.Publish(ctx, "cancelled", []File{{Path: "one", Contents: "a"}, {Path: "two", Contents: "b"}, {Path: "three", Contents: "c"}})
		cancel()
		if !errors.Is(err, context.Canceled) || got.Published || got.RecoveryPath != "" {
			t.Fatal(checkpoint, got, err)
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 0 {
			t.Fatal("cancelled staging leaked", entries)
		}
	}
}

func TestCancellationPreservesUnknownAndEditedStagingFiles(t *testing.T) {
	root := t.TempDir()
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stage string
	ctx := &cancelAtCheck{Context: base, cancel: cancel, checks: 3, beforeCancel: func() {
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 1 {
			t.Fatal(entries, err)
		}
		stage = filepath.Join(root, entries[0].Name())
		if err := os.WriteFile(filepath.Join(stage, "foreign.txt"), []byte("keep foreign"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, "one"), []byte("user changed generated content"), 0600); err != nil {
			t.Fatal(err)
		}
	}}
	got, err := parent.Publish(ctx, "cancelled", []File{{Path: "one", Contents: "generated"}, {Path: "two", Contents: "not written"}})
	if !errors.Is(err, context.Canceled) || got.Published || got.RecoveryPath != stage {
		t.Fatal(got, err)
	}
	for name, expected := range map[string]string{"one": "user changed generated content", "foreign.txt": "keep foreign"} {
		data, err := os.ReadFile(filepath.Join(stage, name))
		if err != nil || string(data) != expected {
			t.Fatal("unowned content removed", name, err)
		}
	}
}

func TestRejectsSymlinkParentsTraversalAndMovedParent(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "parent")
	os.Mkdir(root, 0700)
	alias := filepath.Join(base, "alias")
	os.Symlink(root, alias)
	for _, path := range []string{"/", "relative", root + "/../parent", alias} {
		if parent, err := OpenParent(path); err == nil {
			parent.Close()
			t.Fatalf("accepted %s", path)
		}
	}
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, files := range [][]File{{{Path: "../escape", Contents: "bad"}}, {{Path: "dup", Contents: "one"}, {Path: "dup", Contents: "two"}}, {{Path: "nested/../escape", Contents: "bad"}}, {{Path: "src", Contents: "file"}, {Path: "src/index.ts", Contents: "collision"}}, {{Path: "/absolute", Contents: "bad"}}} {
		if _, err := parent.Publish(context.Background(), "safe", files); !errors.Is(err, ErrOptions) {
			t.Fatal(err)
		}
	}
	os.Rename(root, root+"-moved")
	os.Mkdir(root, 0700)
	got, err := parent.Publish(context.Background(), "safe", []File{{Path: "file", Contents: "content"}})
	if !errors.Is(err, ErrChanged) || got.Published {
		t.Fatal(got, err)
	}
	for _, path := range []string{root, root + "-moved"} {
		entries, _ := os.ReadDir(path)
		if len(entries) != 0 {
			t.Fatal("moved parent changed", entries)
		}
	}
}

func TestNestedPublicationAndCancellation(t *testing.T) {
	files := []File{{Path: "src/index.ts", Contents: "entry"}, {Path: "src/lib/value.ts", Contents: "value"}, {Path: "README.md", Contents: "readme"}}
	root := t.TempDir()
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, checkpoint := range []int{2, 3, 4, 5} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtCheck{Context: base, cancel: cancel, checks: checkpoint}
		result, err := parent.Publish(ctx, "cancelled", files)
		cancel()
		if !errors.Is(err, context.Canceled) || result.Published || result.RecoveryPath != "" {
			t.Fatal(checkpoint, result, err)
		}
		entries, _ := os.ReadDir(root)
		if len(entries) != 0 {
			t.Fatal("nested cancellation left staging", entries)
		}
	}
	result, err := parent.Publish(context.Background(), "nested", files)
	if err != nil || !result.Published || !result.Durable {
		t.Fatal(result, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, "nested", file.Path))
		if err != nil || string(data) != file.Contents {
			t.Fatal(file.Path, err)
		}
	}
	for _, directory := range []string{"src", "src/lib"} {
		info, err := os.Stat(filepath.Join(root, "nested", directory))
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal(directory, info, err)
		}
	}
}

func TestNestedCleanupDoesNotFollowReplacedDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	external := filepath.Join(outside, "index.ts")
	if err := os.WriteFile(external, []byte("keep external"), 0600); err != nil {
		t.Fatal(err)
	}
	parent, err := OpenParent(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stage string
	ctx := &cancelAtCheck{Context: base, cancel: cancel, checks: 3, beforeCancel: func() {
		entries, _ := os.ReadDir(root)
		stage = filepath.Join(root, entries[0].Name())
		if err := os.Rename(filepath.Join(stage, "src"), filepath.Join(stage, "moved-src")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(stage, "src")); err != nil {
			t.Fatal(err)
		}
	}}
	result, err := parent.Publish(ctx, "safe", []File{{Path: "src/index.ts", Contents: "generated"}, {Path: "src/next.ts", Contents: "next"}})
	if !errors.Is(err, context.Canceled) || result.Published || result.RecoveryPath != stage {
		t.Fatal(result, err)
	}
	if contents, _ := os.ReadFile(external); string(contents) != "keep external" {
		t.Fatal("cleanup followed replacement symlink")
	}
	if info, err := os.Lstat(filepath.Join(stage, "src")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("unknown entry removed", info, err)
	}
}

func TestUnexpectedNestedDirectoryIsNotAdoptedOrPublished(t *testing.T) {
	for _, scenario := range []string{"existing-directory", "symlink", "unexpected-file"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			parent, err := OpenParent(root)
			if err != nil {
				t.Fatal(err)
			}
			defer parent.Close()
			var stage string
			ctx := &cancelAtCheck{Context: context.Background(), cancel: func() {}, checks: 2, beforeCancel: func() {
				entries, _ := os.ReadDir(root)
				stage = filepath.Join(root, entries[0].Name())
				switch scenario {
				case "existing-directory":
					err = os.Mkdir(filepath.Join(stage, "src"), 0700)
				case "symlink":
					err = os.Symlink(outside, filepath.Join(stage, "src"))
				default:
					err = os.WriteFile(filepath.Join(stage, "foreign.txt"), []byte("keep"), 0600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}}
			result, err := parent.Publish(ctx, "safe", []File{{Path: "src/index.ts", Contents: "reviewed"}})
			if err == nil || result.Published || result.RecoveryPath != stage {
				t.Fatal(result, err)
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 0 {
				t.Fatal("write escaped staging", entries)
			}
		})
	}
}
