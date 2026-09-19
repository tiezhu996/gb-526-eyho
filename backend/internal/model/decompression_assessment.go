package model

import (
	"time"

	"commercial-diving-decompression-control/backend/internal/constants"
)

type DecompressionAssessment struct {
	ID                   uint               `gorm:"primaryKey" json:"id"`
	PlanID               uint               `gorm:"not null;index" json:"plan_id"`
	AssessmentStatus     string             `gorm:"size:40;not null;index;check:assessment_status IN ('modeled','pending_supervisor_review','approved_for_training','archived')" json:"assessment_status"`
	AlgorithmVersion     string             `gorm:"size:48;not null;index" json:"algorithm_version"`
	InputSnapshotJSON    string             `gorm:"type:text;not null" json:"input_snapshot_json"`
	CompartmentLoadsJSON string             `gorm:"type:text;not null" json:"compartment_loads_json"`
	RiskFlagsJSON        string             `gorm:"type:text;not null" json:"risk_flags_json"`
	HighestRiskBand      constants.RiskBand `gorm:"size:20;not null;default:'informational';index;check:highest_risk_band IN ('informational','caution','elevated','invalid')" json:"highest_risk_band"`
	ComparativeScore     float64            `gorm:"not null" json:"comparative_score"`
	AssumptionsJSON      string             `gorm:"type:text;not null" json:"assumptions_json"`
	CreatedAt            time.Time          `gorm:"not null;index" json:"created_at"`
	ReviewedAt           *time.Time         `json:"reviewed_at"`
}

func (DecompressionAssessment) TableName() string { return "decompression_assessments" }

// AssessmentRiskConfirmation records one supervisor confirmation of a snapshot
// risk flag. Rows are inserted in the same transaction as the approval
// transition and are never updated or deleted by ordinary flows; the unique
// index makes repeated or concurrent confirmations take effect at most once.
type AssessmentRiskConfirmation struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	AssessmentID    uint      `gorm:"not null;uniqueIndex:ux_risk_confirmation_assessment_flag" json:"assessment_id"`
	FlagCode        string    `gorm:"size:64;not null;uniqueIndex:ux_risk_confirmation_assessment_flag" json:"flag_code"`
	Band            string    `gorm:"size:20;not null" json:"band"`
	ConfirmedBy     uint      `gorm:"not null;index" json:"confirmed_by"`
	ConfirmedByName string    `gorm:"size:64;not null" json:"confirmed_by_name"`
	ConfirmedAt     time.Time `gorm:"not null" json:"confirmed_at"`
}

func (AssessmentRiskConfirmation) TableName() string { return "assessment_risk_confirmations" }
