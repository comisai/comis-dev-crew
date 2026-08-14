//go:build integration

package integration_test

import (
	"context"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// integrationRuntimeAttachments keeps non-installed integration lanes focused
// on their adapter/restart boundary while satisfying the required preparation
// and activation attachment contract deterministically.
type integrationRuntimeAttachments struct{}

func (integrationRuntimeAttachments) PrepareRuntimeAttachment(
	_ context.Context,
	request application.RuntimeAttachmentPreparationRequest,
) (application.PreparedRuntimeAttachment, error) {
	return application.PreparedRuntimeAttachment{
		Kind:       application.RuntimeAttachmentUnixSocket,
		SourcePath: filepath.Join("/private/integration-runtime", request.TaskHandle, "attachment.sock"),
	}, nil
}

func (integrationRuntimeAttachments) ReleaseRuntimeAttachment(context.Context, string) error {
	return nil
}

func (integrationRuntimeAttachments) BindRuntimeAttachment(
	context.Context,
	application.RuntimeAttachmentBindingRequest,
) error {
	return nil
}
