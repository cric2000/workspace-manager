package applications

import (
	"context"
	"errors"
	"testing"

	"workspace-manager/internal/domain"
)

type fixtureInstances struct{ state string }

func (fixture *fixtureInstances) Check(context.Context, string) InstanceStatus {
	return InstanceStatus{State: fixture.state, Message: "fixture observation"}
}

func TestDuplicatePolicyRechecksIdentityAndNeverGuesses(t *testing.T) {
	for _, state := range []string{"running", "not_running", "unknown"} {
		for _, policy := range []domain.DuplicatePolicy{domain.AlwaysLaunch, domain.SkipIfRunning} {
			t.Run(state+string(policy), func(t *testing.T) {
				fixture := launchFixture()
				fixture.spec.Entry.DBusActivatable = true
				calls := 0
				fixture.launch = func(context.Context, LaunchSpec) error { calls++; return nil }
				checker := &fixtureInstances{state: "not_running"}
				service := NewLaunchService(fixture, WithInstanceChecker(checker))
				review := service.ReviewDesktop(context.Background(), fixture.spec.Entry.ID, policy)
				if review.Code != "" || review.Instance.State != "not_running" {
					t.Fatal(review)
				}
				checker.state = state // The status seen at review is never launch authority.
				result := service.Launch(context.Background(), review.Token)
				want := "requested"
				if policy == domain.SkipIfRunning && state == "running" {
					want = "skipped"
				}
				if policy == domain.SkipIfRunning && state == "unknown" {
					want = "instance_unknown"
				}
				if result.Code != want || (calls == 1) != (want == "requested") {
					t.Fatal(result, calls)
				}
				if service.Launch(context.Background(), review.Token) != result {
					t.Fatal("outcome was not retained")
				}
			})
		}
	}
	fixture := launchFixture()
	service := NewLaunchService(fixture, WithInstanceChecker(&fixtureInstances{state: "running"}))
	review := service.ReviewDesktop(context.Background(), fixture.spec.Entry.ID, domain.SkipIfRunning)
	if review.Instance.State != "unknown" || service.Launch(context.Background(), review.Token).Code != "instance_unknown" {
		t.Fatal("non-D-Bus app was matched by ID or name")
	}
}

type fixtureCustomLauncher struct {
	launches    int
	fingerprint string
}

func (launcher *fixtureCustomLauncher) ReviewCustom(_ context.Context, app domain.CustomApplication) (CustomLaunchSpec, error) {
	return CustomLaunchSpec{Application: app, Fingerprint: launcher.fingerprint}, nil
}
func (launcher *fixtureCustomLauncher) LaunchCustom(context.Context, CustomLaunchSpec) error {
	launcher.launches++
	return nil
}

func TestCustomReviewUsesSavedConfigurationAndInvalidatesChangedRevision(t *testing.T) {
	ctx := context.Background()
	custom := domain.CustomApplication{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "Tool", Executable: "/bin/true", WorkingDirectory: "/tmp", Arguments: []string{"literal"}}
	stale := false
	adapter := &fixtureCustomLauncher{fingerprint: "initial"}
	service := NewLaunchService(launchFixture(), WithCustomLauncher(adapter, func(context.Context, CustomTarget) (domain.CustomApplication, error) {
		if stale {
			return domain.CustomApplication{}, errors.New("revision changed")
		}
		return custom, nil
	}))
	review := service.ReviewCustom(ctx, CustomTarget{}, domain.AlwaysLaunch)
	if review.Custom == nil || review.Custom.Executable != custom.Executable || adapter.launches != 0 {
		t.Fatal(review)
	}
	stale = true
	if got := service.Launch(ctx, review.Token); got.Code != "changed" || adapter.launches != 0 {
		t.Fatal(got)
	}
	stale = false
	review = service.ReviewCustom(ctx, CustomTarget{}, domain.AlwaysLaunch)
	adapter.fingerprint = "replaced"
	if got := service.Launch(ctx, review.Token); got.Code != "changed" || adapter.launches != 0 {
		t.Fatal(got)
	}
	review = service.ReviewCustom(ctx, CustomTarget{}, domain.AlwaysLaunch)
	if got := service.Launch(ctx, review.Token); got.Code != "requested" || adapter.launches != 1 {
		t.Fatal(got)
	}
	if got := service.Launch(ctx, review.Token); got.Code != "requested" || adapter.launches != 1 {
		t.Fatal("custom request replayed", got)
	}
}
