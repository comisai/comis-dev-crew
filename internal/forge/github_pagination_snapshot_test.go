package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestGitHubAdapterTreatsMutablePaginationAsUnknown(t *testing.T) {
	head := strings.Repeat("d", 40)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		requests++
		page := request.URL.Query().Get("page")
		pass := (requests - 1) / 2
		var runs []map[string]any
		switch {
		case pass == 0 && page == "1":
			runs = append(runs, checkRunForSnapshot(1, "ci/unit", "completed", "success"))
			for index := 0; index < 99; index++ {
				runs = append(runs, checkRunForSnapshot(1000+index, fmt.Sprintf("ci/extra-%d", index), "completed", "success"))
			}
		case pass == 0 && page == "2":
			runs = []map[string]any{checkRunForSnapshot(1099, "ci/extra-99", "completed", "success")}
		case pass == 1 && page == "1":
			runs = append(runs, checkRunForSnapshot(2, "ci/unit", "completed", "failure"))
			runs = append(runs, checkRunForSnapshot(1, "ci/unit", "completed", "success"))
			for index := 1; index < 99; index++ {
				runs = append(runs, checkRunForSnapshot(1000+index, fmt.Sprintf("ci/extra-%d", index), "completed", "success"))
			}
		case pass == 1 && page == "2":
			runs = []map[string]any{checkRunForSnapshot(1099, "ci/extra-99", "completed", "success")}
		default:
			http.Error(response, "unexpected pagination request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"total_count": 101, "check_runs": runs})
	}))
	defer server.Close()

	adapter, err := NewGitHubAdapter(validGitHubConfig(server))
	if err != nil {
		t.Fatal(err)
	}
	checks, err := adapter.readChecks(context.Background(), "read-token", head, []string{"ci/unit"})
	if err != nil {
		t.Fatalf("readChecks(mutable pagination) error = %v", err)
	}
	want := []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckUnknown}}
	if !reflect.DeepEqual(checks, want) || requests != 4 {
		t.Fatalf("readChecks(mutable pagination) = %#v after %d requests, want %#v after 4", checks, requests, want)
	}
}

func checkRunForSnapshot(id int, name, status, conclusion string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "status": status, "conclusion": conclusion,
		"started_at": "2026-08-16T12:00:00Z",
	}
}
