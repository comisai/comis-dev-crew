package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestGitHubAdapterTreatsMalformedCheckRunEnvelopeAsUnknown(t *testing.T) {
	head := strings.Repeat("8", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"total_count":3,"check_runs":[` +
			`{"id":51,"name":"ci/unit","status":"completed","conclusion":"success","started_at":"2026-08-14T20:00:00Z"},` +
			`42,[{"name":"ci/unit"}]]}`))
	}))
	defer server.Close()
	adapter, err := NewGitHubAdapter(validGitHubConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := adapter.readChecks(context.Background(), "read-token", head, []string{"ci/unit"})
	if err != nil {
		t.Fatalf("readChecks(malformed envelope) error = %v", err)
	}
	want := []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckUnknown}}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("readChecks(malformed envelope) = %#v, want %#v", checks, want)
	}
}

func TestGitHubAdapterTreatsDuplicateCheckRunFieldsAsUnknown(t *testing.T) {
	head := strings.Repeat("7", 40)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"total_count":2,"check_runs":[` +
			`{"id":61,"name":"ci/unit","status":"completed","conclusion":"failure","conclusion":"success","started_at":"2026-08-14T20:00:00Z"},` +
			`{"id":62,"name":"ci/lint","status":"completed","conclusion":"success","started_at":null,"started_at":"2026-08-14T20:00:00Z"}]}`))
	}))
	defer server.Close()
	adapter, err := NewGitHubAdapter(validGitHubConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := adapter.readChecks(context.Background(), "read-token", head, []string{"ci/unit", "ci/lint"})
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ForgeCheckEvidence{
		{Name: "ci/unit", Conclusion: domain.CheckUnknown},
		{Name: "ci/lint", Conclusion: domain.CheckUnknown},
	}
	if !reflect.DeepEqual(checks, want) {
		t.Fatalf("readChecks(duplicate fields) = %#v, want %#v", checks, want)
	}
}
