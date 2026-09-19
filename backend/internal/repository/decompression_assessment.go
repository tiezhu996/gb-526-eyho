package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"commercial-diving-decompression-control/backend/internal/audit"
	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/model"
	"commercial-diving-decompression-control/backend/internal/util"
	"gorm.io/gorm"
)

// concurrentLockError reports whether err is the SQLite shared-cache write
// lock raised when two transactions write the same row at once. PostgreSQL
// serializes on the row lock and instead reaches the RowsAffected guard below;
// this translation keeps the smoke SQLite store returning a clean 409 rather
// than a 500 when a concurrent approval wins the guarded update.
func concurrentLockError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

type DecompressionAssessmentRepository struct {
	db    *gorm.DB
	audit *audit.Repository
	// writeMu serializes status-transition critical sections. SQLite shared-cache
	// databases allow only one writer and even block concurrent readers, so the
	// mutex makes the loser re-read the post-commit state and fail the optimistic
	// guard with a clean conflict. PostgreSQL relies on its row lock; the mutex is
	// only a local serialization aid and never weakens the version predicate.
	writeMu sync.Mutex
}

func NewDecompressionAssessmentRepository(db *gorm.DB, auditRepo *audit.Repository) *DecompressionAssessmentRepository {
	return &DecompressionAssessmentRepository{db: db, audit: auditRepo}
}

// LockTransitions acquires the repository-wide transition lock. The returned
// function must be deferred. Callers that need read-then-write atomicity on the
// SQLite smoke store hold it across both phases.
func (r *DecompressionAssessmentRepository) LockTransitions() func() {
	r.writeMu.Lock()
	return r.writeMu.Unlock
}

func (r *DecompressionAssessmentRepository) List(ctx context.Context, planID uint, status string, page, size int) ([]model.DecompressionAssessment, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.DecompressionAssessment{})
	if planID > 0 {
		query = query.Where("plan_id = ?", planID)
	}
	if status != "" {
		query = query.Where("assessment_status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count assessments: %w", err)
	}
	var items []model.DecompressionAssessment
	if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list assessments: %w", err)
	}
	return items, total, nil
}

func (r *DecompressionAssessmentRepository) Get(ctx context.Context, id uint) (model.DecompressionAssessment, error) {
	var item model.DecompressionAssessment
	if err := r.db.WithContext(ctx).First(&item, id).Error; err != nil {
		return model.DecompressionAssessment{}, fmt.Errorf("get assessment %d: %w", id, err)
	}
	return item, nil
}

func (r *DecompressionAssessmentRepository) LatestByPlan(ctx context.Context, planID uint) (model.DecompressionAssessment, error) {
	var item model.DecompressionAssessment
	if err := r.db.WithContext(ctx).Where("plan_id = ?", planID).Order("created_at DESC, id DESC").First(&item).Error; err != nil {
		return model.DecompressionAssessment{}, fmt.Errorf("get latest assessment for plan %d: %w", planID, err)
	}
	return item, nil
}

// ListRiskAcksByAssessments returns persisted supervisor risk acknowledgments
// grouped by assessment ID. Assessments without acknowledgments are absent from
// the map rather than mapped to nil.
func (r *DecompressionAssessmentRepository) ListRiskAcksByAssessments(ctx context.Context, assessmentIDs []uint) (map[uint][]model.RiskAcknowledgment, error) {
	grouped := make(map[uint][]model.RiskAcknowledgment)
	if len(assessmentIDs) == 0 {
		return grouped, nil
	}
	var acks []model.RiskAcknowledgment
	if err := r.db.WithContext(ctx).Where("assessment_id IN ?", assessmentIDs).Order("assessment_id ASC, id ASC").Find(&acks).Error; err != nil {
		return nil, fmt.Errorf("list risk acknowledgments: %w", err)
	}
	for _, ack := range acks {
		grouped[ack.AssessmentID] = append(grouped[ack.AssessmentID], ack)
	}
	return grouped, nil
}

func (r *DecompressionAssessmentRepository) CreateModeled(ctx context.Context, plan model.DivePlan, item *model.DecompressionAssessment, entry audit.Entry) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.DivePlan{}).Where("id = ? AND version = ? AND plan_status = ?", plan.ID, plan.Version, constants.PlanDraft).Updates(map[string]any{"plan_status": constants.PlanModeled, "version": gorm.Expr("version + 1")})
		if result.Error != nil {
			return fmt.Errorf("mark plan modeled: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return util.Conflict("PLAN_VERSION_CONFLICT", "plan must remain at the requested draft version", nil)
		}
		if err := tx.Create(item).Error; err != nil {
			return fmt.Errorf("create immutable assessment: %w", err)
		}
		entry.EntityID = item.ID
		if err := r.audit.RecordWithDB(ctx, tx, entry); err != nil {
			return err
		}
		planEntry := entry
		planEntry.Action = "dive_plan.transition"
		planEntry.EntityType = "dive_plan"
		planEntry.EntityID = plan.ID
		planEntry.BeforeSummary = string(constants.PlanDraft)
		planEntry.AfterSummary = fmt.Sprintf("%s assessment=%d", constants.PlanModeled, item.ID)
		return r.audit.RecordWithDB(ctx, tx, planEntry)
	})
	if err != nil {
		return fmt.Errorf("create modeled assessment transaction: %w", err)
	}
	return nil
}

func (r *DecompressionAssessmentRepository) Transition(ctx context.Context, plan model.DivePlan, assessment model.DecompressionAssessment, target constants.PlanStatus, actorID uint, at time.Time, acks []model.RiskAcknowledgment, entry audit.Entry) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if at.IsZero() {
			at = time.Now().UTC()
		}
		planChanges := map[string]any{"plan_status": target, "version": gorm.Expr("version + 1")}
		assessmentChanges := map[string]any{"assessment_status": string(target)}
		if target == constants.PlanApprovedTraining {
			planChanges["reviewed_by"] = actorID
			assessmentChanges["reviewed_at"] = at
		}
		// Guarded optimistic-lock updates run first so that a duplicate or
		// concurrent approval aborts on the state/version predicate before any
		// risk acknowledgment or audit row is written.
		planResult := tx.Model(&model.DivePlan{}).Where("id = ? AND version = ? AND plan_status = ?", plan.ID, plan.Version, plan.PlanStatus).Updates(planChanges)
		if planResult.Error != nil {
			if concurrentLockError(planResult.Error) {
				return util.Conflict("PLAN_VERSION_CONFLICT", "plan state or version changed concurrently", planResult.Error)
			}
			return fmt.Errorf("transition assessment plan: %w", planResult.Error)
		}
		if planResult.RowsAffected != 1 {
			return util.Conflict("PLAN_VERSION_CONFLICT", "plan state or version changed concurrently", nil)
		}
		assessmentResult := tx.Model(&model.DecompressionAssessment{}).Where("id = ? AND assessment_status = ?", assessment.ID, assessment.AssessmentStatus).Updates(assessmentChanges)
		if assessmentResult.Error != nil {
			if concurrentLockError(assessmentResult.Error) {
				return util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment review state changed concurrently", assessmentResult.Error)
			}
			return fmt.Errorf("transition assessment metadata: %w", assessmentResult.Error)
		}
		if assessmentResult.RowsAffected != 1 {
			return util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment review state changed concurrently", nil)
		}
		// Only the transaction that won the state guard reaches this point, so the
		// per-flag evidence is written exactly once within the same transaction.
		if target == constants.PlanApprovedTraining {
			for index := range acks {
				acks[index].AssessmentID = assessment.ID
				if acks[index].AckAt.IsZero() {
					acks[index].AckAt = at
				}
				if err := tx.Create(&acks[index]).Error; err != nil {
					if errors.Is(err, gorm.ErrDuplicatedKey) {
						return util.Conflict("RISK_ACK_DUPLICATE", "risk was already confirmed for this assessment", err)
					}
					return fmt.Errorf("record risk acknowledgment: %w", err)
				}
			}
		}
		entry.EntityID = assessment.ID
		if err := r.audit.RecordWithDB(ctx, tx, entry); err != nil {
			return err
		}
		planEntry := entry
		planEntry.EntityType = "dive_plan"
		planEntry.EntityID = plan.ID
		planEntry.Action = "dive_plan.transition"
		return r.audit.RecordWithDB(ctx, tx, planEntry)
	})
	if err != nil {
		return fmt.Errorf("transition assessment transaction: %w", err)
	}
	return nil
}
