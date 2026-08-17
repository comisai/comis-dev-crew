package livecampaign

import "testing"

// The rendered status table is SERVICE, HEALTH, COMPLETENESS, and STATE VERSION, so every
// accepted row carries its state version beside the health and completeness columns.
func TestRollbackServiceStatusAcceptsHealthyDatabaseOnlyMode(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{
			name:   "database-only mode",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  partial       83\n",
			want:   true,
		},
		{
			name:   "fully configured mode",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  complete      12\n",
			want:   true,
		},
		{
			name:   "first state version",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  partial       0\n",
			want:   true,
		},
		{
			name:   "substring lookalikes",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  unhealthy  incomplete  83\n",
			want:   false,
		},
		{
			name:   "unavailable state",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  unavailable  83\n",
			want:   false,
		},
		{
			name:   "absent state version",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  partial\n",
			want:   false,
		},
		{
			name:   "non-numeric state version",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\ndevcrew-service  healthy  partial       unknown\n",
			want:   false,
		},
		{
			name:   "other service row",
			status: "SERVICE          HEALTH   COMPLETENESS  STATE VERSION\nother-service    healthy  complete      83\n",
			want:   false,
		},
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
