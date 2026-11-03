package lifecycle

import (
	"context"
	"os"
	"time"
	"workspace-manager/internal/domain"
)

const selectionID domain.ID = "00000000000000000000000000000003"

func (c *Controller) saveSelection(ctx context.Context, plan Plan) error {
	snapshot := domain.Session{ID: selectionID, Name: "Known launch metadata", CapturedAt: time.Now().UTC(), WorkspaceID: plan.WorkspaceID, Projects: plan.Projects, Applications: plan.Applications, URLs: plan.URLs, CaptureWarnings: []string{}}
	value := domain.ManagedSelection{ID: selectionID, OriginSessionID: plan.SessionID, Snapshot: snapshot}
	old, err := c.selection.Get(ctx, selectionID)
	if os.IsNotExist(err) {
		return c.selection.Create(ctx, value)
	}
	if err != nil {
		return err
	}
	return c.selection.Update(ctx, selectionID, old.Revision, func(domain.ManagedSelection) (domain.ManagedSelection, error) { return value, nil })
}
