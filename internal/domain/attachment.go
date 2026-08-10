package domain

import "regexp"

var attachmentTargetNamePattern = regexp.MustCompile(`^attachment-[a-f0-9]{32}\.sock$`)

// ValidateAttachmentTargetName rejects names outside the Comis-managed
// execution-attachment mount vocabulary.
func ValidateAttachmentTargetName(value string) error {
	if !attachmentTargetNamePattern.MatchString(value) {
		return &ValidationError{Field: "attachmentTargetName", Reason: "must be a Comis-managed attachment socket name"}
	}
	return nil
}
