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
	RiskReview       RiskReview                       `json:"risk_review"`
	CreatedAt        time.Time                        `json:"created_at"`
	ReviewedAt       *time.Time                       `json:"reviewed_at"`
	SafetyDisclaimer string                           `json:"safety_disclaimer"`
}

// RiskReviewItem is the per-flag supervisor confirmation state of one
// snapshot risk flag, readable again after approval.
type RiskReviewItem struct {
	Code        string             `json:"code"`
	Band        constants.RiskBand `json:"band"`
	Required    bool               `json:"required"`
	Confirmed   bool               `json:"confirmed"`
	ConfirmedBy string             `json:"confirmed_by,omitempty"`
	ConfirmedAt *time.Time         `json:"confirmed_at,omitempty"`
}

// RiskReview summarizes supervisor confirmation progress for a snapshot.
type RiskReview struct {
	Items         []RiskReviewItem `json:"items"`
	RequiredCount int              `json:"required_count"`
	PendingCount  int              `json:"pending_count"`
	ConfirmedBy   string           `json:"confirmed_by,omitempty"`
	ConfirmedAt   *time.Time       `json:"confirmed_at,omitempty"`
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
	return DecodeAssessmentWithReview(item, nil)
}

// DecodeAssessmentWithReview decodes the immutable assessment and folds the
// persisted supervisor confirmations into the per-flag review state.
func DecodeAssessmentWithReview(item model.DecompressionAssessment, confirmations []model.AssessmentRiskConfirmation) (AssessmentResponse, error) {
	response := AssessmentResponse{ID: item.ID, PlanID: item.PlanID, AssessmentStatus: item.AssessmentStatus, AlgorithmVersion: item.AlgorithmVersion, HighestRiskBand: item.HighestRiskBand, ComparativeScore: item.ComparativeScore, CreatedAt: item.CreatedAt, ReviewedAt: item.ReviewedAt, SafetyDisclaimer: SafetyDisclaimer}
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
	response.RiskReview = BuildRiskReview(response.RiskFlags, confirmations)
	return response, nil
}

// BuildRiskReview merges snapshot risk flags with persisted confirmations so
// the review page can show the confirmer, time, pending count, and per-flag
// state even after the assessment was approved.
func BuildRiskReview(flags []decompression.RiskFlag, confirmations []model.AssessmentRiskConfirmation) RiskReview {
	byCode := make(map[string]model.AssessmentRiskConfirmation, len(confirmations))
	for _, confirmation := range confirmations {
		byCode[confirmation.FlagCode] = confirmation
	}
	review := RiskReview{Items: make([]RiskReviewItem, 0, len(flags))}
	var latest *time.Time
	for _, flag := range flags {
		item := RiskReviewItem{Code: flag.Code, Band: flag.Band, Required: constants.ConfirmationRequired(flag.Band)}
		if !item.Required {
			review.Items = append(review.Items, item)
			continue
		}
		review.RequiredCount++
		confirmation, ok := byCode[flag.Code]
		if !ok {
			review.PendingCount++
			review.Items = append(review.Items, item)
			continue
		}
		item.Confirmed = true
		item.ConfirmedBy = confirmation.ConfirmedByName
		confirmedAt := confirmation.ConfirmedAt
		item.ConfirmedAt = &confirmedAt
		if review.ConfirmedBy == "" {
			review.ConfirmedBy = confirmation.ConfirmedByName
		}
		if latest == nil || confirmedAt.After(*latest) {
			latest = &confirmedAt
		}
		review.Items = append(review.Items, item)
	}
	review.ConfirmedAt = latest
	return review
}
