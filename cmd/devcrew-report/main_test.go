package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRunFailsLoudlyBeforeCommandDispatchWhenMountedClientConstructionFails(t *testing.T) {
	t.Setenv(application.RuntimeAttachmentPathEnvironment,
		"/run/comis/attachments/attachment-0123456789abcdef0123456789abcdef.sock")
	t.Setenv(application.RuntimeAttachmentTargetEnvironment,
		"attachment-ffffffffffffffffffffffffffffffff.sock")
	var stdout, stderr bytes.Buffer
	exit := run(context.Background(), []string{"--help"}, &stdout, &stderr)
	const wantError = "devcrew-report: initialize protected attachment: runtime mounted attachment socket path is invalid\n"
	if exit != 1 || stdout.Len() != 0 || stderr.String() != wantError {
		t.Fatalf("run(construction failure) = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
