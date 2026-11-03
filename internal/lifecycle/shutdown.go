package lifecycle

import (
	"context"
	"time"

	"workspace-manager/internal/domain"
)

func (c *Controller) beginShutdown(ctx context.Context, held *heldPlan, req Request) Response {
	// This is the final, explicit confirmation. Read-only reviews never capture,
	// stop a service or call the desktop shutdown API.
	held.started = true
	c.runtime.ShutdownState = "confirming"
	if req.Save {
		c.runtime.ShutdownState = "capturing"
		name := cleanName(req.Name)
		if name == "" {
			name = "Before shutdown · " + time.Now().Format("2006-01-02 15:04")
		}
		snapshot, err := c.capture(ctx, name, c.runtime.SessionOrigin)
		if err == nil {
			err = c.persistSnapshot(ctx, snapshot)
		}
		if err != nil {
			c.runtime.ShutdownState = "failed"
			held.result = failure("Snapshot could not be saved and verified. Shutdown was not requested and no service was stopped. Review again to choose another action.")
			return held.result
		}
		c.runtime.ShutdownState = "snapshot_saved"
	}
	if ctx.Err() != nil {
		c.runtime.ShutdownState = "cancelled"
		held.result = Response{Code: "cancelled", Message: "Cancelled before stopping services. Any saved snapshot is preserved."}
		return held.result
	}
	c.runtime.Busy = true
	shutdownCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	held.result = Response{Message: "Shutdown workflow accepted. Inspect Activity; cancel before the desktop request to keep the computer running."}
	c.workers.Add(1)
	go func() { defer c.workers.Done(); c.shutdown(shutdownCtx) }()
	return held.result
}
func (c *Controller) shutdownState(state, message string) {
	c.mu.Lock()
	c.runtime.ShutdownState = state
	c.runtime.Message = message
	c.mu.Unlock()
}
func (c *Controller) shutdown(ctx context.Context) {
	defer func() { c.mu.Lock(); c.runtime.Busy = false; c.cancel = nil; c.mu.Unlock() }()
	c.shutdownState("stopping_managed_processes", "Gracefully stopping managed services. Other applications are preserved.")
	err := c.stopAll(ctx)
	if ctx.Err() != nil {
		c.shutdownState("cancelled", "Shutdown cancelled. Any saved snapshot is preserved; already stopped services remain stopped.")
		return
	}
	if err != nil {
		c.shutdownState("failed", "Some managed services did not stop safely. Shutdown was not requested. Inspect Activity; force stop is a separate action.")
		return
	}
	c.shutdownState("requesting_shutdown", "Handing the normal shutdown request to the desktop.")
	if ctx.Err() != nil {
		c.shutdownState("cancelled", "Cancelled before the desktop request.")
		return
	}
	if c.hooks.Shutdown == nil || c.hooks.Shutdown(ctx) != nil {
		c.shutdownState("failed", "The desktop denied or could not accept shutdown. The snapshot is preserved. Normal shutdown dialogs remain under desktop control.")
		return
	}
	c.shutdownState("completed_request", "The desktop accepted the normal shutdown request. This does not confirm that the computer powered off.")
}
func (c *Controller) stopAll(ctx context.Context) error {
	runs, err := c.runs.ListBounded(ctx, 256)
	if err != nil {
		return err
	}
	var ids []domain.ID
	for _, snapshot := range runs {
		run := snapshot.Value
		if terminal(run.State) {
			continue
		}
		if err := signalWorker(run, false); err != nil {
			return err
		}
		ids = append(ids, run.ID)
	}
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	defer cancel()
	for len(ids) > 0 {
		pending := ids[:0]
		for _, id := range ids {
			snapshot, err := c.runs.Get(ctx, id)
			if err != nil {
				return err
			}
			if !terminal(snapshot.Value.State) {
				pending = append(pending, id)
			}
		}
		ids = pending
		if len(ids) == 0 {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
