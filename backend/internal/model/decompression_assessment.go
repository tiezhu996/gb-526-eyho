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

// RiskAcknowledgment is the immutable per-flag evidence a supervisor records
// in the same transaction that approves an assessment. One row exists for each
// caution, elevated, or invalid risk flag present in the assessment snapshot.
type RiskAcknowledgment struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	AssessmentID uint      `gorm:"not null;uniqueIndex:idx_risk_ack_assessment_code,priority:1;index" json:"assessment_id"`
	RiskCode     string    `gorm:"size:80;not null;uniqueIndex:idx_risk_ack_assessment_code,priority:2" json:"risk_code"`
	RiskBand     string    `gorm:"size:20;not null" json:"risk_band"`
	AckBy        uint      `gorm:"not null;index" json:"ack_by"`
	AckUsername  string    `gorm:"size:64;not null" json:"ack_username"`
	AckAt        time.Time `gorm:"not null;index" json:"ack_at"`
}

func (RiskAcknowledgment) TableName() string { return "risk_acknowledgments" }
