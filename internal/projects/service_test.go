package projects

import (
	"context"
	"errors"
	"sync"
	"testing"

	"workspace-manager/internal/projects/detection"
)

type fakeInspector func(context.Context, string) (detection.Report, error)

func (fake fakeInspector) Inspect(ctx context.Context, path string) (detection.Report, error) {
	return fake(ctx, path)
}

func TestInspectionFacadeReturnsOnlySafeMessages(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{detection.ErrDirectory, "directory"}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {errors.New("credential=private /private/path"), "inspection_failed"}} {
		service := NewService(fakeInspector(func(context.Context, string) (detection.Report, error) { return detection.Report{}, test.err }))
		got := service.Inspect(context.Background(), "/selected")
		if got.Report != nil || got.Code != test.code || got.Message == test.err.Error() {
			t.Fatal(got)
		}
	}
	service := NewService(fakeInspector(func(_ context.Context, path string) (detection.Report, error) {
		return detection.Report{Path: path}, nil
	}))
	if got := service.Inspect(context.Background(), "/selected"); got.Code != "" || got.Report == nil || got.Report.Path != "/selected" {
		t.Fatal(got)
	}
}

func TestInspectionSlotsAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 2)
	service := NewService(fakeInspector(func(ctx context.Context, _ string) (detection.Report, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return detection.Report{}, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			if got := service.Inspect(ctx, "/selected"); got.Code != "cancelled" {
				t.Error(got)
			}
		})
	}
	<-entered
	<-entered
	if got := service.Inspect(context.Background(), "/third"); got.Code != "busy" {
		t.Fatal(got)
	}
	cancel()
	group.Wait()
	if len(service.slots) != 0 {
		t.Fatal("cancelled inspection retained a slot")
	}
}
