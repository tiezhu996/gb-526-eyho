package service

import (
	"errors"
	"testing"

	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/decompression"
	"commercial-diving-decompression-control/backend/internal/dto"
	"commercial-diving-decompression-control/backend/internal/util"
)

func appErrorCode(err error, code string) bool {
	var appErr *util.AppError
	return errors.As(err, &appErr) && appErr.Code == code
}

func TestValidateConfirmations(t *testing.T) {
	flags := []decompression.RiskFlag{
		{Code: "TRAINING_ONLY", Band: constants.RiskInformational},
		{Code: "DEPTH_ASSUMPTION_CAUTION", Band: constants.RiskCaution},
		{Code: "O2_PARTIAL_ASSUMPTION_ELEVATED", Band: constants.RiskElevated},
	}
	ack := func(code string, band constants.RiskBand) dto.RiskConfirmation {
		return dto.RiskConfirmation{RiskCode: code, RiskBand: band}
	}
	tests := []struct {
		name          string
		confirmations []dto.RiskConfirmation
		wantErr       bool
		wantCode      string
	}{
		{name: "exact set", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated)}, wantErr: false},
		{name: "order independent", confirmations: []dto.RiskConfirmation{ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated), ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution)}, wantErr: false},
		{name: "one missing", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution)}, wantErr: true, wantCode: "RISK_ACK_SET_MISMATCH"},
		{name: "one extra", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated), ack("UNKNOWN_FLAG", constants.RiskCaution)}, wantErr: true, wantCode: "RISK_ACK_SET_MISMATCH"},
		{name: "informational not required", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated), ack("TRAINING_ONLY", constants.RiskInformational)}, wantErr: true, wantCode: "RISK_ACK_SET_MISMATCH"},
		{name: "duplicate code", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution), ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskCaution), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated)}, wantErr: true, wantCode: "RISK_ACK_DUPLICATE"},
		{name: "wrong band", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", constants.RiskElevated), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated)}, wantErr: true, wantCode: "RISK_ACK_BAND_MISMATCH"},
		{name: "invalid band value", confirmations: []dto.RiskConfirmation{ack("DEPTH_ASSUMPTION_CAUTION", "catastrophic"), ack("O2_PARTIAL_ASSUMPTION_ELEVATED", constants.RiskElevated)}, wantErr: true, wantCode: "RISK_ACK_INVALID"},
		{name: "empty when none required", confirmations: []dto.RiskConfirmation{}, wantErr: false},
	}
	// Last case requires a snapshot without caution/elevated/invalid flags.
	flagSets := map[string][]decompression.RiskFlag{
		"empty when none required": {{Code: "TRAINING_ONLY", Band: constants.RiskInformational}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := flags
			if override, ok := flagSets[test.name]; ok {
				snapshot = override
			}
			err := validateConfirmations(snapshot, test.confirmations)
			if test.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if test.wantErr && test.wantCode != "" && !appErrorCode(err, test.wantCode) {
				t.Fatalf("expected error code %s, got %v", test.wantCode, err)
			}
		})
	}
}

func TestCountUnconfirmed(t *testing.T) {
	flags := []decompression.RiskFlag{
		{Code: "INFO", Band: constants.RiskInformational},
		{Code: "CAUTION_A", Band: constants.RiskCaution},
		{Code: "CAUTION_B", Band: constants.RiskCaution},
		{Code: "ELEVATED_A", Band: constants.RiskElevated},
		{Code: "INVALID_A", Band: constants.RiskInvalid},
	}
	if got := countUnconfirmed(flags, map[string]bool{}); got != 4 {
		t.Fatalf("unconfirmed with no acks = %d, want 4", got)
	}
	if got := countUnconfirmed(flags, map[string]bool{"CAUTION_A": true, "ELEVATED_A": true, "INVALID_A": true}); got != 1 {
		t.Fatalf("partial acks unconfirmed = %d, want 1", got)
	}
	if got := countUnconfirmed(flags, map[string]bool{"CAUTION_A": true, "CAUTION_B": true, "ELEVATED_A": true, "INVALID_A": true}); got != 0 {
		t.Fatalf("all acked unconfirmed = %d, want 0", got)
	}
	// Confirming an informational flag never reduces the actionable count.
	if got := countUnconfirmed(flags, map[string]bool{"INFO": true}); got != 4 {
		t.Fatalf("informational ack unconfirmed = %d, want 4", got)
	}
}
