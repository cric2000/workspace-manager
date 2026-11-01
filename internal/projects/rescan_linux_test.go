package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"workspace-manager/internal/domain"
	"workspace-manager/internal/projects/detection"
	"workspace-manager/internal/storage"
)

func comparisonTarget(view ProjectView) ProjectTarget {
	return ProjectTarget{ID: view.Project.ID, Revision: view.Revision}
}

func TestRescanMatchesInvocationWithoutWritingOrChangingApproval(t *testing.T) {
	service, draft, paths := registryFixture(t)
	configureSettings(&draft)
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	filename := filepath.Join(paths.ProjectsDir(), string(view.Project.ID)+".json")
	before, _ := os.ReadFile(filename)
	service.inspector = fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		return detection.Report{Path: path, Candidates: []detection.Candidate{
			{Label: "Matching command with another label", Program: draft.Commands[0].Configuration.Executable, Arguments: draft.Commands[0].Configuration.Arguments, WorkingDirectory: path},
			{Label: "Different arguments", Program: draft.Commands[0].Configuration.Executable, Arguments: []string{"new", ""}, WorkingDirectory: path},
			{Label: "Missing tool", Program: "missing-workspace-rescan-tool", Arguments: []string{"new"}, WorkingDirectory: path},
		}}, nil
	})
	result := service.RescanProject(context.Background(), comparisonTarget(view))
	if result.Code != "" || result.Comparison == nil {
		t.Fatal(result)
	}
	comparison := result.Comparison
	if !reflect.DeepEqual(comparison.Saved, view) || comparison.ScannedAt.IsZero() {
		t.Fatal("saved configuration changed", comparison.Saved)
	}
	if !reflect.DeepEqual(comparison.Candidates[0].MatchingCommandIDs, []domain.ID{view.Project.Commands[0].ID}) {
		t.Fatal(comparison.Candidates)
	}
	if !comparison.Candidates[1].Available || len(comparison.Candidates[1].MatchingCommandIDs) != 0 || comparison.Candidates[2].Available || len(comparison.Candidates[2].MatchingCommandIDs) != 0 {
		t.Fatal(comparison.Candidates)
	}
	comparison.Saved.Project.Commands[0].Configuration.Runtime.EnvironmentFiles[0] = "mutated.env"
	comparison.Saved.Project.Preferences.Editor.Arguments[0] = "mutated"
	loaded := service.ListProjects(context.Background())
	if !reflect.DeepEqual(loaded.State.Projects[0], view) {
		t.Fatal("returned comparison mutated saved settings")
	}
	after, _ := os.ReadFile(filename)
	if !bytes.Equal(before, after) || len(service.reviews) != 1 {
		t.Fatal("rescan wrote metadata or granted a review")
	}
	history, err := service.registry.History(context.Background(), view.Project.ID)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	if _, err := os.Stat(filepath.Join(view.Project.Path, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("rescan executed a tool")
	}
}

func TestRescanRefusesStaleTargetsBeforeAndAfterInspection(t *testing.T) {
	service, draft, _ := registryFixture(t)
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	service.inspector = fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		calls.Add(1)
		close(entered)
		<-release
		return detection.Report{Path: path}, nil
	})
	stale := comparisonTarget(view)
	stale.Revision = storage.Revision(string(bytes.Repeat([]byte{'b'}, 64)))
	if result := service.RescanProject(context.Background(), stale); result.Code != "conflict" || calls.Load() != 0 {
		t.Fatal(result)
	}
	results := make(chan RescanResult, 1)
	go func() { results <- service.RescanProject(context.Background(), comparisonTarget(view)) }()
	<-entered
	// The same service remains available to edit while the inspector is blocked.
	updated := editDraft(view)
	updated.Name = "Changed during rescan"
	requireSave(t, service, requireReview(t, service, updated))
	close(release)
	if result := <-results; result.Code != "conflict" || result.Comparison != nil {
		t.Fatal(result)
	}
}

func TestRescanErrorsPartialResultsCancellationAndDirectoryReplacement(t *testing.T) {
	service, draft, _ := registryFixture(t)
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	for _, scenario := range []struct {
		err  error
		code string
	}{
		{detection.ErrDirectory, "directory"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {errors.New("secret=value /private/path"), "inspection_failed"},
	} {
		service.inspector = fakeInspector(func(context.Context, string) (detection.Report, error) { return detection.Report{}, scenario.err })
		result := service.RescanProject(context.Background(), comparisonTarget(view))
		if result.Code != scenario.code || result.Comparison != nil || bytes.Contains([]byte(result.Message), []byte("secret=")) {
			t.Fatal(result)
		}
	}
	service.inspector = fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		return detection.Report{Path: path, Partial: true, Findings: []detection.Finding{{Code: "limit", Message: "Inspect a smaller directory."}}}, nil
	})
	if result := service.RescanProject(context.Background(), comparisonTarget(view)); result.Code != "" || !result.Comparison.Report.Partial || len(result.Comparison.Candidates) != 0 {
		t.Fatal(result)
	}
	service.inspector = fakeInspector(func(ctx context.Context, path string) (detection.Report, error) {
		<-ctx.Done()
		return detection.Report{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := service.RescanProject(ctx, comparisonTarget(view)); result.Code != "cancelled" {
		t.Fatal(result)
	}
	service.inspector = fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		if err := os.Rename(path, path+"-previous"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return detection.Report{Path: path}, nil
	})
	if result := service.RescanProject(context.Background(), comparisonTarget(view)); result.Code != "directory_changed" || result.Comparison != nil {
		t.Fatal(result)
	}
}

func TestRescanLocalChangedManifestKeepsApprovedConfiguration(t *testing.T) {
	service, draft, paths := registryFixture(t)
	tool := filepath.Join(filepath.Dir(draft.Path), "npm")
	if err := os.Rename(draft.Commands[0].Configuration.Executable, tool); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(tool))
	draft.Commands[0].Configuration.Executable, draft.Commands[0].Configuration.Arguments = tool, []string{"run", "dev"}
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	filename := filepath.Join(paths.ProjectsDir(), string(view.Project.ID)+".json")
	before, _ := os.ReadFile(filename)
	service.inspector = detection.LocalInspector{}
	manifest := filepath.Join(draft.Path, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"packageManager":"npm@10.0.0","scripts":{"dev":"touch must-not-run"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	first := service.RescanProject(context.Background(), comparisonTarget(view))
	if first.Code != "" || len(first.Comparison.Candidates) != 1 || len(first.Comparison.Candidates[0].MatchingCommandIDs) != 1 {
		t.Fatal(first)
	}
	if err := os.WriteFile(manifest, []byte(`{"packageManager":"npm@10.0.0","scripts":{"start":"touch must-not-run"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	second := service.RescanProject(context.Background(), comparisonTarget(view))
	if second.Code != "" || len(second.Comparison.Candidates) != 1 || len(second.Comparison.Candidates[0].MatchingCommandIDs) != 0 || !reflect.DeepEqual(second.Comparison.Report.Candidates[0].Arguments, []string{"run", "start"}) {
		t.Fatal(second)
	}
	after, _ := os.ReadFile(filename)
	if !bytes.Equal(before, after) {
		t.Fatal("manifest rescan rewrote approval")
	}
	if _, err := os.Stat(filepath.Join(draft.Path, "must-not-run")); !os.IsNotExist(err) {
		t.Fatal("detector ran a project tool or script")
	}
}

func TestRescanValidationAndServiceShutdown(t *testing.T) {
	service, draft, _ := registryFixture(t)
	if result := service.RescanProject(context.Background(), ProjectTarget{ID: "../invalid"}); result.Code != "validation" {
		t.Fatal(result)
	}
	view := requireSave(t, service, requireReview(t, service, draft)).State.Projects[0]
	if result := service.RescanProject(context.Background(), comparisonTarget(view)); result.Code != "inspection_failed" {
		t.Fatal(result)
	}
	service.inspector = fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		return detection.Report{Path: path + "-different"}, nil
	})
	if result := service.RescanProject(context.Background(), comparisonTarget(view)); result.Code != "directory_changed" {
		t.Fatal(result)
	}
	if err := service.ServiceShutdown(); err != nil {
		t.Fatal(err)
	}
	if result := service.RescanProject(context.Background(), comparisonTarget(view)); result.Code != "closed" {
		t.Fatal(result)
	}
}
