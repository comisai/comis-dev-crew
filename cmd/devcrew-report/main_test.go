package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRunFailsLoudlyBeforeCommandDispatchWhenMountedClientConstructionFails(t *testing.T) {
	t.Setenv(application.RuntimeAttachmentPathEnvironment,
		"/run/comis/attachments/attachment-0123456789abcdef0123456789abcdef.sock")
	t.Setenv(application.RuntimeAttachmentTargetEnvironment,
		"attachment-ffffffffffffffffffffffffffffffff.sock")
	var stdout, stderr bytes.Buffer
	exit := run(context.Background(), []string{"brief"}, &stdout, &stderr)
	const wantError = "devcrew-report: initialize protected attachment: runtime mounted attachment socket path is invalid\n"
	if exit != 1 || stdout.Len() != 0 || stderr.String() != wantError {
		t.Fatalf("run(construction failure) = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunExposesMetadataCommandsWithoutAMountedAttachment(t *testing.T) {
	t.Setenv(application.RuntimeAttachmentPathEnvironment, "")
	t.Setenv(application.RuntimeAttachmentTargetEnvironment, "")
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
