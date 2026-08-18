package application

import (
	"context"
	"errors"
)

// ListWorkerProfiles reports the reviewed dispatch catalog and its posture.
//
// A liaison that cannot start work needs to distinguish three answers: no
// profile supports this shape, the profile exists but its harness is
// unavailable, and it is usable but cannot be trusted unattended. None of them
// is inferable from a preparation failure, so the catalog is a read of its own.
func (queries *Queries) ListWorkerProfiles(ctx context.Context) (WorkerProfileList, error) {
	if ctx == nil {
		return WorkerProfileList{}, errors.New("list worker profiles: context is required")
	}
	if err := ctx.Err(); err != nil {
		return WorkerProfileList{}, err
	}
	stateVersion, err := queries.repository.CurrentStateVersion(ctx)
	if err != nil {
		return WorkerProfileList{}, err
	}
	profiles := []WorkerProfileSummary{}
	// Absent catalog means the deployment configured no profile at all, which is
	// exactly the answer the caller asked for — not a failure.
	if queries.workerProfiles != nil {
		if configured := queries.workerProfiles(); configured != nil {
			profiles = configured
		}
	}
	return WorkerProfileList{
		SchemaVersion: 1,
		CapturedAtMs:  queries.now().UTC().UnixMilli(),
		StateVersion:  stateVersion,
		Profiles:      profiles,
	}, nil
}
