package applications

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureLauncher struct {
	spec        LaunchSpec
	reviewError error
	launch      func(context.Context, LaunchSpec) error
}

func (fixture fixtureLauncher) Review(context.Context, string) (LaunchSpec, error) {
	return fixture.spec, fixture.reviewError
}
func (fixture fixtureLauncher) Launch(ctx context.Context, spec LaunchSpec) error {
	return fixture.launch(ctx, spec)
}
func launchFixture() fixtureLauncher {
	return fixtureLauncher{spec: LaunchSpec{Entry: DesktopEntry{ID: "org.example.Editor.desktop", Name: "Editor", Visible: true}, Fingerprint: "server-owned"}, launch: func(context.Context, LaunchSpec) error { return nil }}
}

func TestLaunchRequiresReviewAndReplaysOutcome(t *testing.T) {
	fixture := launchFixture()
	var calls atomic.Int32
	fixture.launch = func(_ context.Context, spec LaunchSpec) error {
		calls.Add(1)
		if spec != fixture.spec {
			t.Error("launch spec did not come from review")
		}
		return nil
	}
	service := NewLaunchService(fixture)
	ctx := context.Background()
	if got := service.Launch(ctx, fixture.spec.Entry.ID); got.Code != "review_required" {
		t.Fatal(got)
	}
	review := service.Review(ctx, fixture.spec.Entry.ID)
	if review.Code != "" || review.Entry == nil || review.Token == "" || calls.Load() != 0 {
		t.Fatal(review)
	}
	review.Entry.Name = "tampered client copy"
	first := service.Launch(ctx, review.Token)
	if first.Code != "requested" || service.Launch(ctx, review.Token) != first || calls.Load() != 1 {
		t.Fatal(first, calls.Load())
	}
}

func TestConcurrentLaunchUsesOneAttempt(t *testing.T) {
	fixture := launchFixture()
	entered, release := make(chan struct{}), make(chan struct{})
	fixture.launch = func(context.Context, LaunchSpec) error { close(entered); <-release; return nil }
	service := NewLaunchService(fixture)
	ctx := context.Background()
	review := service.Review(ctx, fixture.spec.Entry.ID)
	done := make(chan LaunchResult, 1)
	go func() { done <- service.Launch(ctx, review.Token) }()
	<-entered
	if got := service.Launch(ctx, review.Token); got.Code != "in_progress" {
		t.Fatal(got)
	}
	close(release)
	if got := <-done; got.Code != "requested" {
		t.Fatal(got)
	}
}

func TestLaunchReviewExpiryAndCapacity(t *testing.T) {
	fixture := launchFixture()
	service := NewLaunchService(fixture)
	now := time.Now()
	service.now = func() time.Time { return now }
	ctx := context.Background()
	first := service.Review(ctx, fixture.spec.Entry.ID)
	for range 63 {
		if got := service.Review(ctx, fixture.spec.Entry.ID); got.Code != "" {
			t.Fatal(got)
		}
	}
	if got := service.Review(ctx, fixture.spec.Entry.ID); got.Code != "busy" {
		t.Fatal(got)
	}
	now = now.Add(5 * time.Minute)
	if got := service.Launch(ctx, first.Token); got.Code != "review_required" {
		t.Fatal(got)
	}
	if got := service.Review(ctx, fixture.spec.Entry.ID); got.Code != "" || len(service.records) != 1 {
		t.Fatal(got)
	}
}

func TestLaunchFailureResultsAreSafeAndNotRetried(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{ErrLaunchChanged, "changed"}, {ErrUnavailable, "unavailable"},
		{context.Canceled, "cancelled"}, {context.DeadlineExceeded, "cancelled"},
		{errors.New("secret=/private/token"), "failed"},
	} {
		t.Run(test.code+fmt.Sprint(test.err), func(t *testing.T) {
			fixture := launchFixture()
			calls := 0
			fixture.launch = func(context.Context, LaunchSpec) error { calls++; return test.err }
			service := NewLaunchService(fixture)
			token := service.Review(context.Background(), fixture.spec.Entry.ID).Token
			first := service.Launch(context.Background(), token)
			if first.Code != test.code || first.Message == test.err.Error() || first != service.Launch(context.Background(), token) || calls != 1 {
				t.Fatal(first, calls)
			}
		})
	}
}

func TestLaunchCancelledBeforeDispatch(t *testing.T) {
	fixture := launchFixture()
	fixture.launch = func(context.Context, LaunchSpec) error { t.Fatal("cancelled request dispatched"); return nil }
	service := NewLaunchService(fixture)
	token := service.Review(context.Background(), fixture.spec.Entry.ID).Token
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := service.Launch(ctx, token); got.Code != "cancelled" {
		t.Fatal(got)
	}
}

func TestInvalidOrUnavailableReviewDoesNotGrantToken(t *testing.T) {
	for _, change := range []func(*fixtureLauncher){
		func(f *fixtureLauncher) { f.reviewError = errors.New("private path") },
		func(f *fixtureLauncher) { f.spec.Entry.Visible = false },
		func(f *fixtureLauncher) { f.spec.Entry.ID = "other.desktop" },
		func(f *fixtureLauncher) { f.spec.Fingerprint = "" },
	} {
		fixture := launchFixture()
		change(&fixture)
		service := NewLaunchService(fixture)
		if got := service.Review(context.Background(), "org.example.Editor.desktop"); got.Token != "" || got.Code != "unavailable" || len(service.records) != 0 {
			t.Fatal(got)
		}
	}
	if got := NewLaunchService(launchFixture()).Review(context.Background(), "../../bad.desktop"); got.Token != "" || got.Code != "unavailable" {
		t.Fatal(got)
	}
}
