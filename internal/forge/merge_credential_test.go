package forge

import "testing"

func TestMergeCredentialIsASeparateAuthorityFromPush(t *testing.T) {
	merge := Credential{
		Kind:   CredentialMerge,
		Secret: "merge-identity",
		Scopes: []CredentialScope{ScopePullRequestsWrite},
	}
	if !validMergeCredential(merge) {
		t.Fatal("a correctly scoped merge credential was refused")
	}
	// A push identity must never satisfy the merge check. If it did, every
	// worker that can push a branch could merge it, which is exactly the
	// separation merge_after_approval exists to keep.
	push := Credential{
		Kind:   CredentialPush,
		Secret: "push-identity",
		Scopes: []CredentialScope{ScopeContentsWrite},
	}
	if validMergeCredential(push) {
		t.Fatal("a push credential satisfied the merge check")
	}
	if validPushCredential(merge) {
		t.Fatal("a merge credential satisfied the push check")
	}
}

func TestMergeCredentialRefusesAWideningScopeSet(t *testing.T) {
	for _, scopes := range [][]CredentialScope{
		{},
		{ScopeContentsWrite},
		{ScopePullRequestsWrite, ScopeContentsWrite},
	} {
		credential := Credential{Kind: CredentialMerge, Secret: "merge-identity", Scopes: scopes}
		if validMergeCredential(credential) {
			t.Fatalf("merge credential accepted scopes %v", scopes)
		}
	}
}

func TestMergeCredentialRefusesAnUnusableSecret(t *testing.T) {
	for _, secret := range []string{"", "has space", "has\nnewline"} {
		credential := Credential{
			Kind:   CredentialMerge,
			Secret: secret,
			Scopes: []CredentialScope{ScopePullRequestsWrite},
		}
		if validMergeCredential(credential) {
			t.Fatalf("merge credential accepted secret %q", secret)
		}
	}
}
