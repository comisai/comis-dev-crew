package service

import (
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/writerlock"
)

// writerAuthorityFailure classifies a refused data-directory claim. Contention
// is an expected outcome with an exact operator repair, so it carries a closed
// code and hint rather than reading as an internal fault. The contended path
// stays in the private cause: the rendered message names the condition only.
func writerAuthorityFailure(cause error) error {
	if !errors.Is(cause, writerlock.ErrHeld) {
		return fmt.Errorf("run service writer authority: %w", cause)
	}
	failure, err := domain.NewFailure(
		domain.ErrorConflict,
		true,
		"another running service instance owns this data directory",
		"stop the running service instance before starting another against the same data directory",
		cause,
	)
	if err != nil {
		return errors.New("run service: data directory writer authority is unavailable")
	}
	return failure
}

// acquireWriterAuthority claims the data directory before the store is opened.
//
// Every step between the claim and the endpoint bind writes: migration, runtime
// relay identity recovery, startup reconciliation, and validation recovery. A
// claim taken any later would let a second instance convert the running
// instance's work to unknown on its way to discovering the endpoint was taken.
func acquireWriterAuthority(databasePath string) (*writerlock.Lock, error) {
	lock, err := writerlock.Acquire(databasePath)
	if err != nil {
		return nil, writerAuthorityFailure(err)
	}
	return lock, nil
}
