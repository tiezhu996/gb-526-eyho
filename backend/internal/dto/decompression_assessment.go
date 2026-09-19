package dto

import (
	"encoding/json"
	"fmt"
	"time"

	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/decompression"
	"commercial-diving-decompression-control/backend/internal/model"
)

type RunAssessmentRequest struct {
	PlanVersion uint `json:"plan_version" binding:"required,min=1"`
}

// RiskConfirmation is a single supervisor acknowledgment. Code and band must
// match one caution/elevated/invalid flag from the immutable assessment snapshot.
type RiskConfirmation struct {
	RiskCode string             `json:"risk_code" binding:"required,max=80"`
	RiskBand constants.RiskBand `json:"risk_band" binding:"required"`
}

type RiskAcknowledgmentResponse struct {
	RiskCode    string    `json:"risk_code"`
	RiskBand    string    `json:"risk_band"`
	AckBy       uint      `json:"ack_by"`
	AckUsername string    `json:"ack_username"`
	AckAt       time.Time `json:"ack_at"`
}

// ApproveAssessmentRequest is the supervisor approval body. The confirmations
// set must contain exactly the caution, elevated and invalid flags in the
// snapshot: one extra, one missing, or a duplicate entry is rejected.
type ApproveAssessmentRequest struct {
	TargetStatus  constants.PlanStatus `json:"target_status" binding:"required"`
	Version       uint                 `json:"version" binding:"required,min=1"`
	Reason        string               `json:"reason" binding:"required,min=3,max=300"`
	Confirmations []RiskConfirmation   `json:"confirmations" binding:"required"`
}

type AssessmentResponse struct {
	ID               uint                             `json:"id"`
	PlanID           uint                             `json:"plan_id"`
	AssessmentStatus string                           `json:"assessment_status"`
	AlgorithmVersion string                           `json:"algorithm_version"`
	InputSnapshot    decompression.InputSnapshot      `json:"input_snapshot"`
	CompartmentLoads []decompression.CompartmentCurve `json:"compartment_loads"`
	RiskFlags        []decompression.RiskFlag         `json:"risk_flags"`
	HighestRiskBand  constants.RiskBand               `json:"highest_risk_band"`
	ComparativeScore float64                          `json:"comparative_score"`
	Assumptions      decompression.ModelAssumptions   `json:"assumptions"`
	CreatedAt        time.Time                        `json:"created_at"`
	ReviewedAt       *time.Time                       `json:"reviewed_at"`
	RiskAcks         []RiskAcknowledgmentResponse     `json:"risk_acks"`
	UnconfirmedCount int                              `json:"unconfirmed_count"`
	SafetyDisclaimer string                           `json:"safety_disclaimer"`
}

type AssessmentComparison struct {
	Left       AssessmentResponse `json:"left"`
	Right      AssessmentResponse `json:"right"`
	ScoreDelta float64            `json:"score_delta"`
	FlagDelta  int                `json:"flag_delta"`
	Summary    []string           `json:"summary"`
	Disclaimer string             `json:"disclaimer"`
}

const SafetyDisclaimer = "Training and decision support only. This result is not medical advice, a certified dive table, a safety clearance, or an executable decompression instruction. Human supervisor review is required."

func DecodeAssessment(item model.DecompressionAssessment) (AssessmentResponse, error) {
	response := AssessmentResponse{ID: item.ID, PlanID: item.PlanID, AssessmentStatus: item.AssessmentStatus, AlgorithmVersion: item.AlgorithmVersion, HighestRiskBand: item.HighestRiskBand, ComparativeScore: item.ComparativeScore, CreatedAt: item.CreatedAt, ReviewedAt: item.ReviewedAt, RiskAcks: []RiskAcknowledgmentResponse{}, SafetyDisclaimer: SafetyDisclaimer}
	parts := []struct {
		name string
		raw  string
		to   any
	}{
		{"input snapshot", item.InputSnapshotJSON, &response.InputSnapshot},
		{"compartment loads", item.CompartmentLoadsJSON, &response.CompartmentLoads},
		{"risk flags", item.RiskFlagsJSON, &response.RiskFlags},
		{"assumptions", item.AssumptionsJSON, &response.Assumptions},
	}
	for _, part := range parts {
		if err := json.Unmarshal([]byte(part.raw), part.to); err != nil {
			return AssessmentResponse{}, fmt.Errorf("decode assessment %d %s: %w", item.ID, part.name, err)
		}
	}
	return response, nil
}
