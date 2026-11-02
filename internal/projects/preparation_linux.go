package projects

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"workspace-manager/internal/projects/bootstrap"
	"workspace-manager/internal/storage"
)

// PrepareCreation is a separate explicit approval boundary. It can download and
// execute only the held scaffolder invocation, in its own private directory.
// It never publishes files, saves a project or installs project dependencies.
func (service *Service) PrepareCreation(ctx context.Context, token string) CreationReviewResult {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return creationReviewFailure(storage.ErrClosed)
	}
	pending := service.creations[token]
	if pending == nil || pending.review.Scaffold == nil {
		service.mu.Unlock()
		return creationReviewFailure(errReview)
	}
	if pending.preparation != nil {
		result := clonePreparationResult(*pending.preparation)
		service.mu.Unlock()
		return result
	}
	if pending.preparing {
		service.mu.Unlock()
		return preparationPending()
	}
	if err := service.checkPreparation(ctx, pending); err != nil {
		service.mu.Unlock()
		return creationReviewFailure(err)
	}
	select {
	case service.preparationSlot <- struct{}{}:
	default:
		service.mu.Unlock()
		return CreationReviewResult{Code: "busy", Message: "Another scaffolder is preparing files. Wait for it to finish, then try again."}
	}
	log, err := service.beginCreationHistory(ctx, pending, "preparation")
	if err != nil {
		<-service.preparationSlot
		result := CreationReviewResult{Code: "creation_history", Message: "Preparation did not start because its attempt could not be saved. Check app state storage, then request a new review."}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result = creationReviewFailure(err)
		}
		pending.preparation = &result
		service.mu.Unlock()
		return result
	}
	defer log.Close()
	// Persisting the attempt may wait for storage. Recheck tools and destination
	// after that wait and before any external command can run.
	if err := service.checkPreparation(ctx, pending); err != nil {
		<-service.preparationSlot
		result := creationReviewFailure(err)
		result.History = finishCreationHistory(ctx, log, preparationLogResult(result, bootstrap.CommandResult{ExitCode: -1}, 0), service.now().UTC(), token)
		pending.preparation = &result
		service.mu.Unlock()
		return clonePreparationResult(result)
	}
	runCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	pending.preparing, pending.cancel = true, cancel
	action := *pending.review.Scaffold
	action.Arguments = slices.Clone(action.Arguments)
	name := pending.project.Name
	run := service.scaffolder
	service.mu.Unlock()
	files, command, err := run(runCtx, action, name)
	if err == nil {
		err = runCtx.Err()
	}
	if err == nil && len(files) == 0 {
		err = bootstrap.ErrScaffold
	}
	cancel()
	service.mu.Lock()
	defer service.mu.Unlock()
	<-service.preparationSlot
	pending.preparing, pending.cancel = false, nil
	result := CreationReviewResult{}
	if service.closed {
		err = storage.ErrClosed
	}
	if err != nil {
		result = creationReviewFailure(err)
	} else {
		pending.review.Plan.Files = slices.Clone(files)
		pending.review.Prepared = true
		pending.review.ExpiresAt = service.now().UTC().Add(5 * time.Minute)
		review := cloneCreationReview(pending.review)
		result.Review = &review
	}
	result.History = finishCreationHistory(ctx, log, preparationLogResult(result, command, len(files)), service.now().UTC(), token)
	pending.preparation = &result
	return clonePreparationResult(result)
}

func (service *Service) checkPreparation(ctx context.Context, pending *pendingCreation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !service.now().Before(pending.review.ExpiresAt) {
		return errReview
	}
	parent, err := bootstrap.OpenParent(filepath.Dir(pending.review.Path))
	if err != nil {
		return errReview
	}
	defer parent.Close()
	if parent.Identity != pending.parent {
		return errReview
	}
	if err := parent.Absent(pending.project.Name); err != nil {
		return err
	}
	for _, tool := range pending.executables {
		current, err := identity(tool.Path, true)
		if err != nil || current != tool {
			return errReview
		}
	}
	return nil
}

// GetPreparationOutcome never executes or retries preparation after cancellation
// or transport loss. Only a completed held review can move to publication.
func (service *Service) GetPreparationOutcome(ctx context.Context, token string) CreationReviewResult {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return creationReviewFailure(err)
	}
	if service.closed {
		return creationReviewFailure(storage.ErrClosed)
	}
	pending := service.creations[token]
	if pending == nil || pending.review.Scaffold == nil {
		return creationReviewFailure(errReview)
	}
	if pending.preparation != nil {
		return clonePreparationResult(*pending.preparation)
	}
	return preparationPending()
}
func preparationPending() CreationReviewResult {
	return CreationReviewResult{Code: "preparation_pending", Message: "No completed preparation outcome is available. Check again before starting another review. No project has been published."}
}
func clonePreparationResult(result CreationReviewResult) CreationReviewResult {
	if result.History != nil {
		history := *result.History
		result.History = &history
	}
	if result.Review != nil {
		review := cloneCreationReview(*result.Review)
		result.Review = &review
	}
	return result
}
