package livecampaign

import "testing"

func TestRollbackServiceStatusAcceptsHealthyDatabaseOnlyMode(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "database-only mode", status: "SERVICE HEALTH COMPLETENESS\ndevcrew-service healthy partial\n", want: true},
		{name: "fully configured mode", status: "SERVICE HEALTH COMPLETENESS\ndevcrew-service healthy complete\n", want: true},
		{name: "substring lookalikes", status: "SERVICE HEALTH COMPLETENESS\ndevcrew-service unhealthy incomplete\n", want: false},
		{name: "unavailable state", status: "SERVICE HEALTH COMPLETENESS\ndevcrew-service healthy unavailable\n", want: false},
		{name: "malformed output", status: "healthy but not a service status", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rollbackServiceStatusAccepted([]byte(test.status)); got != test.want {
				t.Fatalf("rollbackServiceStatusAccepted() = %t, want %t", got, test.want)
			}
		})
	}
}
