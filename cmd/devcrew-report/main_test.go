package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestReporterConsumesHostManagedAttachmentEnvironment(t *testing.T) {
	if application.RuntimeAttachmentPathEnvironment != "COMIS_EXECUTION_ATTACHMENT" ||
		application.RuntimeAttachmentTargetEnvironment != "COMIS_EXECUTION_ATTACHMENT_TARGET_NAME" ||
		application.RuntimeAttachmentIdentityEnvironment != "COMIS_EXECUTION_ATTACHMENT_IDENTITY" {
		t.Fatalf("runtime attachment environment = %q, %q, %q",
			application.RuntimeAttachmentPathEnvironment, application.RuntimeAttachmentTargetEnvironment,
			application.RuntimeAttachmentIdentityEnvironment)
	}
}

func TestRunFailsLoudlyBeforeCommandDispatchWhenMountedClientConstructionFails(t *testing.T) {
	t.Setenv("COMIS_EXECUTION_ATTACHMENT",
		"/run/comis/attachments/attachment-0123456789abcdef0123456789abcdef.sock")
	t.Setenv("COMIS_EXECUTION_ATTACHMENT_TARGET_NAME",
		"attachment-ffffffffffffffffffffffffffffffff.sock")
	t.Setenv("COMIS_EXECUTION_ATTACHMENT_IDENTITY", strings.Repeat("ab", 32))
	var stdout, stderr bytes.Buffer
	exit := run(context.Background(), []string{"brief"}, &stdout, &stderr)
	const wantError = "devcrew-report: initialize protected attachment: runtime mounted attachment socket path is invalid\n"
	if exit != 1 || stdout.Len() != 0 || stderr.String() != wantError {
		t.Fatalf("run(construction failure) = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunExposesMetadataCommandsWithoutAMountedAttachment(t *testing.T) {
	t.Setenv("COMIS_EXECUTION_ATTACHMENT", "")
	t.Setenv("COMIS_EXECUTION_ATTACHMENT_TARGET_NAME", "")
	t.Setenv("COMIS_EXECUTION_ATTACHMENT_IDENTITY", "")
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "Usage: devcrew-report"},
		{name: "short help", args: []string{"-h"}, want: "Usage: devcrew-report"},
		{name: "version", args: []string{"--version"}, want: "devcrew-report"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := run(context.Background(), test.args, &stdout, &stderr)
			if exit != 0 || !strings.Contains(stdout.String(), test.want) || stderr.Len() != 0 {
				t.Fatalf("run(%q) = %d, stdout=%q stderr=%q", test.args, exit, stdout.String(), stderr.String())
			}
		})
	}
}
