package decompression

import (
	"testing"

	"commercial-diving-decompression-control/backend/internal/constants"
)

func TestRequiredConfirmations(t *testing.T) {
	tests := []struct {
		name  string
		flags []RiskFlag
		want  []string
	}{
		{
			name: "only caution elevated invalid require confirmation",
			flags: []RiskFlag{
				{Code: "TRAINING_ONLY", Band: constants.RiskInformational},
				{Code: "O2_PARTIAL_ASSUMPTION_ELEVATED", Band: constants.RiskElevated},
				{Code: "DEPTH_ASSUMPTION_CAUTION", Band: constants.RiskCaution},
				{Code: "INPUT_BOUNDARY_INVALID", Band: constants.RiskInvalid},
			},
			want: []string{"DEPTH_ASSUMPTION_CAUTION", "INPUT_BOUNDARY_INVALID", "O2_PARTIAL_ASSUMPTION_ELEVATED"},
		},
		{
			name:  "informational only needs no confirmation",
			flags: []RiskFlag{{Code: "TRAINING_ONLY", Band: constants.RiskInformational}},
			want:  []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := RequiredConfirmations(test.flags)
			if len(got) != len(test.want) {
				t.Fatalf("required confirmations = %v, want %v", got, test.want)
			}
			for index := range test.want {
				if got[index] != test.want[index] {
					t.Fatalf("required confirmations = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestValidateConfirmationSet(t *testing.T) {
	required := []string{"DEPTH_ASSUMPTION_CAUTION", "O2_PARTIAL_ASSUMPTION_ELEVATED"}
	tests := []struct {
		name      string
		required  []string
		submitted []string
		wantErr   bool
	}{
		{"exact match accepted", required, []string{"DEPTH_ASSUMPTION_CAUTION", "O2_PARTIAL_ASSUMPTION_ELEVATED"}, false},
		{"order independent", required, []string{"O2_PARTIAL_ASSUMPTION_ELEVATED", "DEPTH_ASSUMPTION_CAUTION"}, false},
		{"one missing rejected", required, []string{"DEPTH_ASSUMPTION_CAUTION"}, true},
		{"empty submission rejected", required, nil, true},
		{"one extra rejected", required, []string{"DEPTH_ASSUMPTION_CAUTION", "O2_PARTIAL_ASSUMPTION_ELEVATED", "TRANSITION_RATE_CAUTION"}, true},
		{"unknown code rejected", required, []string{"DEPTH_ASSUMPTION_CAUTION", "UNKNOWN_FLAG"}, true},
		{"duplicate rejected", required, []string{"DEPTH_ASSUMPTION_CAUTION", "DEPTH_ASSUMPTION_CAUTION"}, true},
		{"blank code rejected", required, []string{"DEPTH_ASSUMPTION_CAUTION", "  "}, true},
		{"no required flags accepts empty", nil, nil, false},
		{"no required flags rejects extras", nil, []string{"DEPTH_ASSUMPTION_CAUTION"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateConfirmationSet(test.required, test.submitted)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateConfirmationSet(%v, %v) error = %v, wantErr %t", test.required, test.submitted, err, test.wantErr)
			}
		})
	}
}
