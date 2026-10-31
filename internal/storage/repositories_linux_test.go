package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"workspace-manager/internal/domain"
)

func entityRepository[T Entity](t *testing.T) (*Repository[T], string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records")
	repository, err := OpenRepository(path, codecFor[T](t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, path
}

func settingsRepository(t *testing.T) (*SettingsRepository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	repository, err := OpenSettingsRepository(path, codecFor[domain.Settings](t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository, path
}

func workspaceFixture() domain.Workspace {
	project := projectFixture()
	return domain.Workspace{
		ID: "11111111111111111111111111111111", Name: "Coding",
		ProjectIDs: []domain.ID{project.ID}, CreatedAt: project.CreatedAt, UpdatedAt: project.UpdatedAt,
	}
}

func sessionFixture() domain.Session {
	return domain.Session{
		ID: "33333333333333333333333333333333", Name: "Friday",
		CapturedAt: projectFixture().CreatedAt, Projects: []domain.Project{projectFixture()},
	}
}

func TestWorkspaceRepositoryCRUDStaleEditsAndUndelete(t *testing.T) {
	ctx := context.Background()
	repository, _ := entityRepository[domain.Workspace](t)
	workspace := workspaceFixture()
	if _, err := repository.Get(ctx, workspace.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing record was hidden: %v", err)
	}
	if err := repository.Create(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, workspace); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("duplicate create was accepted: %v", err)
	}
	original, err := repository.Get(ctx, workspace.ID)
	if err != nil || !reflect.DeepEqual(original.Value, workspace) || !validRevision(original.Revision) {
		t.Fatalf("bad created record: %+v / %v", original, err)
	}
	if err := repository.Update(ctx, workspace.ID, original.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		value.Name = "Backend"
		value.UpdatedAt = value.UpdatedAt.Add(time.Minute)
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := repository.Update(ctx, workspace.ID, original.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		called = true
		return value, nil
	}); !errors.Is(err, ErrConflict) || called {
		t.Fatal("stale edit ran its callback or overwrote a newer record", err)
	}
	list, err := repository.List(ctx)
	if err != nil || len(list) != 1 || list[0].Value.Name != "Backend" {
		t.Fatalf("list failed: %+v / %v", list, err)
	}
	old, err := repository.GetRevision(ctx, workspace.ID, original.Revision)
	if err != nil || old.Value.Name != "Coding" {
		t.Fatalf("previous revision was lost: %+v / %v", old, err)
	}
	latest := list[0]
	if err := repository.Delete(ctx, workspace.ID, original.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("stale delete was accepted", err)
	}
	if err := repository.Delete(ctx, workspace.ID, latest.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx, workspace.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("metadata was not deleted", err)
	}
	if err := repository.RestoreRevision(ctx, workspace.ID, "", latest.Revision); err != nil {
		t.Fatal("explicit undelete failed", err)
	}
	recovered, err := repository.Get(ctx, workspace.ID)
	if err != nil || !reflect.DeepEqual(recovered, latest) {
		t.Fatal("undelete did not restore exact saved bytes", err)
	}
}

func TestProjectDeletionNeverTouchesTheSourceDirectory(t *testing.T) {
	ctx := context.Background()
	repository, _ := entityRepository[domain.Project](t)
	project := projectFixture()
	project.Path = t.TempDir()
	marker := filepath.Join(project.Path, "source.go")
	if err := os.WriteFile(marker, []byte("user source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Get(ctx, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Delete(ctx, project.ID, snapshot.Revision); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "user source" {
		t.Fatal("metadata deletion changed the source directory")
	}
}

func TestSessionsAreImmutableByDefaultAndBranchesRemainIndependent(t *testing.T) {
	ctx := context.Background()
	repository, _ := entityRepository[domain.Session](t)
	original := sessionFixture()
	if err := repository.Create(ctx, original); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Get(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Update(ctx, original.ID, snapshot.Revision, func(value domain.Session) (domain.Session, error) {
		value.Name = "Silent overwrite"
		return value, nil
	}); !errors.Is(err, ErrImmutable) {
		t.Fatal("ordinary update changed an immutable session", err)
	}
	branch, err := original.Branch("Experiment", original.CapturedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	branch.Projects[0].Name = "Experiment API"
	if err := repository.Create(ctx, branch); err != nil {
		t.Fatal(err)
	}
	if err := ReviseSession(ctx, repository, original.ID, snapshot.Revision, func(value domain.Session) (domain.Session, error) {
		value.Name = "Renamed explicitly"
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	old, err := repository.GetRevision(ctx, original.ID, snapshot.Revision)
	if err != nil || !reflect.DeepEqual(old.Value, original) {
		t.Fatal("explicit session revision did not preserve history", err)
	}
	storedBranch, err := repository.Get(ctx, branch.ID)
	if err != nil || storedBranch.Value.ParentSessionID != original.ID || storedBranch.Value.Projects[0].Name != "Experiment API" {
		t.Fatal("branch identity or configuration changed", err)
	}
	list, err := repository.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatal("both sessions must remain independently readable", err)
	}
	orphan := branch
	orphan.ID = "44444444444444444444444444444444"
	orphan.ParentSessionID = "55555555555555555555555555555555"
	if err := repository.Create(ctx, orphan); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("created a branch with an unavailable parent", err)
	}
}

func TestExplicitRecoveryPreservesDamagedBytesAndVerifiesHistory(t *testing.T) {
	ctx := context.Background()
	repository, path := entityRepository[domain.Session](t)
	session := sessionFixture()
	if err := repository.Create(ctx, session); err != nil {
		t.Fatal(err)
	}
	good, err := repository.Get(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	damaged := []byte(`{"schemaVersion":1,"kind":"session","data":`)
	filename := filepath.Join(path, string(session.ID)+".json")
	if err := os.WriteFile(filename, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = repository.Get(ctx, session.ID)
	var readError *ReadError
	if !errors.As(err, &readError) || !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("corruption was hidden", err)
	}
	if err := ReviseSession(ctx, repository, session.ID, readError.Revision, func(value domain.Session) (domain.Session, error) {
		t.Fatal("callback must not run on a corrupt record")
		return value, nil
	}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("ordinary revision overwrote corruption", err)
	}
	if err := repository.RestoreRevision(ctx, session.ID, good.Revision, good.Revision); !errors.Is(err, ErrConflict) {
		t.Fatal("recovery ignored a stale damaged-file fingerprint", err)
	}
	if err := repository.RestoreRevision(ctx, session.ID, readError.Revision, good.Revision); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.Get(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(recovered, good) {
		t.Fatal("failed to restore the last good snapshot", err)
	}
	quarantine := filepath.Join(path, "history", string(session.ID), "damaged-"+string(readError.Revision)+".json")
	data, err := os.ReadFile(quarantine)
	if err != nil || !bytes.Equal(data, damaged) {
		t.Fatal("recovery discarded damaged bytes")
	}
	history, err := repository.History(ctx, session.ID)
	if err != nil || len(history) != 1 || history[0] != good.Revision {
		t.Fatalf("quarantine was treated as a valid snapshot: %v / %v", history, err)
	}
	backup := filepath.Join(path, "history", string(session.ID), string(good.Revision)+".json")
	if err := os.WriteFile(backup, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRevision(ctx, session.ID, good.Revision); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("accepted history with a forged fingerprint", err)
	}
	if err := repository.RestoreRevision(ctx, session.ID, good.Revision, good.Revision); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("restored a damaged backup", err)
	}
}

func TestSettingsRefuseNewerSchemasForEveryMutation(t *testing.T) {
	ctx := context.Background()
	repository, path := settingsRepository(t)
	if _, err := repository.Get(ctx); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing settings were silently replaced with defaults", err)
	}
	if err := repository.Create(ctx, domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	good, err := repository.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(path, "settings.json")
	raw, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	future := bytes.Replace(raw, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 2`), 1)
	if err := os.WriteFile(filename, future, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal(err)
	}
	if err := repository.Update(ctx, revisionOf(future), func(value domain.Settings) (domain.Settings, error) {
		t.Fatal("must not edit unsupported settings")
		return value, nil
	}); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal(err)
	}
	if err := repository.RestoreRevision(ctx, revisionOf(future), good.Revision); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatal("recovery downgraded a newer schema", err)
	}
	if err := repository.Create(ctx, domain.DefaultSettings()); !errors.Is(err, fs.ErrExist) {
		t.Fatal("create replaced unsupported settings", err)
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(future, after) {
		t.Fatal("unsupported settings changed")
	}
}

func TestRejectedEditsAndFailedBackupPreserveTheCurrentRecord(t *testing.T) {
	ctx := context.Background()
	repository, path := entityRepository[domain.Workspace](t)
	workspace := workspaceFixture()
	if err := repository.Create(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Get(ctx, workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("edit refused")
	for _, change := range []func(domain.Workspace) (domain.Workspace, error){
		func(value domain.Workspace) (domain.Workspace, error) { value.Name = ""; return value, nil },
		func(value domain.Workspace) (domain.Workspace, error) {
			value.ID = "44444444444444444444444444444444"
			return value, nil
		},
		func(value domain.Workspace) (domain.Workspace, error) {
			value.CreatedAt = value.CreatedAt.Add(-time.Hour)
			return value, nil
		},
		func(value domain.Workspace) (domain.Workspace, error) { return value, failure },
	} {
		if err := repository.Update(ctx, workspace.ID, snapshot.Revision, change); err == nil {
			t.Fatal("accepted an invalid edit")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	if err := repository.Update(cancelled, workspace.ID, snapshot.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		cancel()
		value.Name = "Must not commit"
		return value, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("committed after callback cancelled the operation", err)
	}
	backup := filepath.Join(path, "history", string(workspace.ID), string(snapshot.Revision)+".json")
	if err := os.Chmod(backup, 0o644); err != nil {
		t.Fatal(err)
	}
	err = repository.Update(ctx, workspace.ID, snapshot.Revision, func(value domain.Workspace) (domain.Workspace, error) {
		value.Name = "Cannot back up"
		return value, nil
	})
	var mutation *MutationError
	if !errors.As(err, &mutation) || mutation.Committed || mutation.Stage != "backup" {
		t.Fatal("backup failure did not abort the primary mutation", err)
	}
	current, err := repository.Get(ctx, workspace.ID)
	if err != nil || !reflect.DeepEqual(current, snapshot) {
		t.Fatal("failed edit or backup changed the original", err)
	}
}

func TestRepositoryDetectsFilenameMismatchAndNeverReturnsPartialLists(t *testing.T) {
	ctx := context.Background()
	repository, path := entityRepository[domain.Workspace](t)
	workspace := workspaceFixture()
	if err := repository.Create(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	wrongID := domain.ID("44444444444444444444444444444444")
	data, err := repository.store.codec.Encode(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, string(wrongID)+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx, wrongID); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("accepted a record whose ID differs from its filename", err)
	}
	list, err := repository.List(ctx)
	if !errors.Is(err, ErrInvalidDocument) || list != nil {
		t.Fatal("returned a misleading partial list", list, err)
	}
}

func TestRepositoryCloseAndCancelledOperations(t *testing.T) {
	repository, _ := settingsRepository(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repository.Create(ctx, domain.DefaultSettings()); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal("close must be idempotent", err)
	}
	if _, err := repository.Get(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal("operation used a closed repository", err)
	}
}

func TestSeparateProcessesDoNotLoseRepositoryUpdates(t *testing.T) {
	repository, path := settingsRepository(t)
	if err := repository.Create(context.Background(), domain.DefaultSettings()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, len(commands))
	for index := range commands {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRepositoryProcessWorker$")
		command.Env = append(os.Environ(), "WORKSPACE_REPOSITORY_TEST_CHILD=1", "WORKSPACE_REPOSITORY_TEST_PATH="+path)
		command.Stdout, command.Stderr = &outputs[index], &outputs[index]
		commands[index] = command
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				_ = command.Process.Kill()
				_ = command.Wait()
			}
		})
	}
	for index, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("worker failed: %v\n%s", err, outputs[index].String())
		}
	}
	value, err := repository.Get(context.Background())
	if err != nil || value.Value.InitialDelaySeconds != 20 {
		t.Fatalf("lost updates across processes: %+v / %v", value, err)
	}
}

func TestRepositoryProcessWorker(t *testing.T) {
	if os.Getenv("WORKSPACE_REPOSITORY_TEST_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	repository, err := OpenSettingsRepository(os.Getenv("WORKSPACE_REPOSITORY_TEST_PATH"), codecFor[domain.Settings](t))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for count := 0; count < 10; {
		snapshot, err := repository.Get(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = repository.Update(ctx, snapshot.Revision, func(value domain.Settings) (domain.Settings, error) {
			value.InitialDelaySeconds++
			return value, nil
		})
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
	}
}

func TestRepositoryInvalidKeysDoNotEscapeStorage(t *testing.T) {
	repository, _ := entityRepository[domain.Project](t)
	ctx := context.Background()
	for _, id := range []domain.ID{"../other", "/absolute", "", "not-an-id"} {
		if _, err := repository.Get(ctx, id); err == nil {
			t.Fatal("accepted invalid record ID")
		}
	}
	project := projectFixture()
	if err := repository.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRevision(ctx, project.ID, Revision(strings.Repeat("../", 22))); err == nil {
		t.Fatal("accepted a traversal revision")
	}
}

func TestRepositoryMigrationsBackUpTheOriginalSchemaBytes(t *testing.T) {
	codec, err := NewCodec[domain.Settings](2, map[int]Migration{
		1: func(data json.RawMessage) (json.RawMessage, error) {
			return bytes.Replace(data, []byte(`"system"`), []byte(`"light"`), 1), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config")
	repository, err := OpenSettingsRepository(path, codec)
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	filename := filepath.Join(path, "settings.json")
	if err := os.WriteFile(filename, settingsV1, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.Get(context.Background())
	if err != nil || snapshot.Value.Appearance != domain.AppearanceLight {
		t.Fatal("repository did not use its configured migration", err)
	}
	afterRead, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(afterRead, settingsV1) {
		t.Fatal("read rewrote the old schema")
	}
	if err := repository.Update(context.Background(), snapshot.Revision, func(value domain.Settings) (domain.Settings, error) {
		value.Appearance = domain.AppearanceDark
		return value, nil
	}); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(path, "history/settings", string(snapshot.Revision)+".json"))
	if err != nil || !bytes.Equal(old, settingsV1) {
		t.Fatal("migration save did not preserve the exact old schema bytes")
	}
	current, err := os.ReadFile(filename)
	if err != nil || !bytes.Contains(current, []byte(`"schemaVersion": 2`)) {
		t.Fatal("explicit save did not write the current schema")
	}
}

func TestRepositoryCommitStatusRefersToThePrimaryRecord(t *testing.T) {
	for _, backupFailure := range []bool{true, false} {
		t.Run(fmtBool(backupFailure), func(t *testing.T) {
			repository, _ := settingsRepository(t)
			ctx := context.Background()
			if err := repository.Create(ctx, domain.DefaultSettings()); err != nil {
				t.Fatal(err)
			}
			initial, err := repository.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Make the next old-value archive a new revision, not an already saved
			// initial revision. It must run through the normal atomic writer.
			if err := repository.Update(ctx, initial.Revision, func(value domain.Settings) (domain.Settings, error) {
				value.Appearance = domain.AppearanceLight
				return value, nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := repository.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			realWrite := repository.store.write
			failure := errors.New("simulated directory durability error")
			repository.store.write = func(root *os.Root, name string, data []byte) error {
				if err := realWrite(root, name, data); err != nil {
					return err
				}
				isPrimary := name == "settings.json"
				if isPrimary != backupFailure {
					return &CommitError{Err: failure}
				}
				return nil
			}
			err = repository.Update(ctx, before.Revision, func(value domain.Settings) (domain.Settings, error) {
				value.Appearance = domain.AppearanceDark
				return value, nil
			})
			var mutation *MutationError
			if !errors.As(err, &mutation) || !errors.Is(err, failure) || mutation.Committed == backupFailure {
				t.Fatalf("wrong primary commit status: %+v / %v", mutation, err)
			}
			current, err := repository.Get(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := domain.AppearanceDark
			if backupFailure {
				want = domain.AppearanceLight
			}
			if current.Value.Appearance != want {
				t.Fatalf("visible primary disagrees with commit status: %+v", current)
			}
		})
	}
}

func fmtBool(backup bool) string {
	if backup {
		return "history commit failed"
	}
	return "primary commit failed"
}

func TestSessionCreationRejectsACorruptedAncestryCycle(t *testing.T) {
	repository, _ := entityRepository[domain.Session](t)
	ctx := context.Background()
	first := sessionFixture()
	if err := repository.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, err := first.Branch("Branch", first.CapturedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	first.ParentSessionID = second.ID
	if err := repository.store.codec.Write(repository.store.root, string(first.ID)+".json", first); err != nil {
		t.Fatal(err)
	}
	third, err := second.Branch("Child", second.CapturedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Create(ctx, third); !errors.Is(err, ErrInvalidDocument) {
		t.Fatal("accepted a branch whose ancestor records contain a cycle", err)
	}
}
