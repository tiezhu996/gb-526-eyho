package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"commercial-diving-decompression-control/backend/internal/audit"
	"commercial-diving-decompression-control/backend/internal/constants"
	"commercial-diving-decompression-control/backend/internal/decompression"
	"commercial-diving-decompression-control/backend/internal/dto"
	"commercial-diving-decompression-control/backend/internal/model"
	"commercial-diving-decompression-control/backend/internal/repository"
	"commercial-diving-decompression-control/backend/internal/util"
)

type DecompressionAssessmentService struct {
	assessments  *repository.DecompressionAssessmentRepository
	plans        *repository.DivePlanRepository
	profiles     *repository.DiverProfileRepository
	segments     *repository.ExposureSegmentRepository
	modelVersion string
	maxSegments  int
}

func NewDecompressionAssessmentService(assessments *repository.DecompressionAssessmentRepository, plans *repository.DivePlanRepository, profiles *repository.DiverProfileRepository, segments *repository.ExposureSegmentRepository, modelVersion string, maxSegments int) *DecompressionAssessmentService {
	return &DecompressionAssessmentService{assessments: assessments, plans: plans, profiles: profiles, segments: segments, modelVersion: modelVersion, maxSegments: maxSegments}
}

func (s *DecompressionAssessmentService) List(ctx context.Context, planID uint, status string, page, size int) ([]dto.AssessmentResponse, int64, error) {
	items, total, err := s.assessments.List(ctx, planID, status, page, size)
	if err != nil {
		return nil, 0, err
	}
	responses := make([]dto.AssessmentResponse, 0, len(items))
	for _, item := range items {
		response, decodeErr := dto.DecodeAssessment(item)
		if decodeErr != nil {
			return nil, 0, decodeErr
		}
		responses = append(responses, response)
	}
	if err := s.attachAcks(ctx, responses); err != nil {
		return nil, 0, err
	}
	return responses, total, nil
}

func (s *DecompressionAssessmentService) Get(ctx context.Context, id uint) (dto.AssessmentResponse, error) {
	item, err := s.assessments.Get(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	response, err := dto.DecodeAssessment(item)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	responses := []dto.AssessmentResponse{response}
	if err := s.attachAcks(ctx, responses); err != nil {
		return dto.AssessmentResponse{}, err
	}
	return responses[0], nil
}

// attachAcks populates persisted supervisor acknowledgments and the count of
// caution/elevated/invalid flags that still lack an acknowledgment on each
// response. It is a read-only projection of the immutable snapshot.
func (s *DecompressionAssessmentService) attachAcks(ctx context.Context, responses []dto.AssessmentResponse) error {
	ids := make([]uint, 0, len(responses))
	for _, response := range responses {
		ids = append(ids, response.ID)
	}
	grouped, err := s.assessments.ListRiskAcksByAssessments(ctx, ids)
	if err != nil {
		return err
	}
	for index := range responses {
		response := &responses[index]
		acks := grouped[response.ID]
		response.RiskAcks = make([]dto.RiskAcknowledgmentResponse, 0, len(acks))
		confirmed := make(map[string]bool, len(acks))
		for _, ack := range acks {
			response.RiskAcks = append(response.RiskAcks, dto.RiskAcknowledgmentResponse{RiskCode: ack.RiskCode, RiskBand: ack.RiskBand, AckBy: ack.AckBy, AckUsername: ack.AckUsername, AckAt: ack.AckAt})
			confirmed[ack.RiskCode] = true
		}
		response.UnconfirmedCount = countUnconfirmed(response.RiskFlags, confirmed)
	}
	return nil
}

func countUnconfirmed(flags []decompression.RiskFlag, confirmed map[string]bool) int {
	count := 0
	for _, flag := range flags {
		if requiresAck(flag.Band) && !confirmed[flag.Code] {
			count++
		}
	}
	return count
}

func requiresAck(band constants.RiskBand) bool {
	return band == constants.RiskCaution || band == constants.RiskElevated || band == constants.RiskInvalid
}

// validateConfirmations enforces the exact-set rule: the submitted
// confirmations must equal the caution/elevated/invalid flags in the immutable
// snapshot. One extra, one missing, or one duplicate entry rejects the approval.
func validateConfirmations(flags []decompression.RiskFlag, confirmations []dto.RiskConfirmation) error {
	required := make(map[string]constants.RiskBand)
	for _, flag := range flags {
		if requiresAck(flag.Band) {
			required[flag.Code] = flag.Band
		}
	}
	submitted := make(map[string]constants.RiskBand, len(confirmations))
	for _, confirmation := range confirmations {
		if confirmation.RiskCode == "" {
			return util.BadRequest("RISK_ACK_INVALID", "each confirmation requires a risk_code", nil)
		}
		if !constants.ValidRiskBand(confirmation.RiskBand) {
			return util.BadRequest("RISK_ACK_INVALID", fmt.Sprintf("risk band %q is not supported", confirmation.RiskBand), nil)
		}
		if _, exists := submitted[confirmation.RiskCode]; exists {
			return util.Unprocessable("RISK_ACK_DUPLICATE", fmt.Sprintf("risk %s was confirmed more than once", confirmation.RiskCode), nil)
		}
		submitted[confirmation.RiskCode] = confirmation.RiskBand
	}
	if len(submitted) != len(required) {
		return util.Unprocessable("RISK_ACK_SET_MISMATCH", fmt.Sprintf("exactly %d caution, elevated or invalid risks must be confirmed, got %d", len(required), len(submitted)), nil)
	}
	for code, band := range required {
		submittedBand, ok := submitted[code]
		if !ok {
			return util.Unprocessable("RISK_ACK_SET_MISMATCH", fmt.Sprintf("snapshot risk %s must be confirmed before approval", code), nil)
		}
		if submittedBand != band {
			return util.Unprocessable("RISK_ACK_BAND_MISMATCH", fmt.Sprintf("risk %s must be confirmed as band %s, got %s", code, band, submittedBand), nil)
		}
	}
	for code := range submitted {
		if _, ok := required[code]; !ok {
			return util.Unprocessable("RISK_ACK_SET_MISMATCH", fmt.Sprintf("risk %s is not a caution, elevated or invalid flag in this snapshot", code), nil)
		}
	}
	return nil
}

func (s *DecompressionAssessmentService) Run(ctx context.Context, planID uint, req dto.RunAssessmentRequest, actor audit.Entry) (dto.AssessmentResponse, error) {
	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	if plan.Version != req.PlanVersion {
		return dto.AssessmentResponse{}, util.Conflict("PLAN_VERSION_CONFLICT", "dive plan was changed by another user", nil)
	}
	if plan.PlanStatus != constants.PlanDraft {
		return dto.AssessmentResponse{}, util.Conflict("PLAN_NOT_DRAFT", "only a draft plan can run a new immutable assessment", nil)
	}
	profile, err := s.profiles.Get(ctx, plan.DiverProfileID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	segments, err := s.segments.ListByPlan(ctx, planID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	result, modelErr := decompression.Run(plan, profile, segments, s.modelVersion, s.maxSegments)
	if modelErr != nil {
		actor.EntityType = "dive_plan"
		_ = s.plans.ResetDraftAfterFailure(ctx, plan, modelErr.Error(), actor)
		return dto.AssessmentResponse{}, util.Unprocessable("MODEL_INPUT_INVALID", modelErr.Error(), modelErr)
	}
	snapshotJSON, curvesJSON, flagsJSON, assumptionsJSON, err := decompression.MarshalResult(result)
	if err != nil {
		return dto.AssessmentResponse{}, util.Internal(err)
	}
	item := model.DecompressionAssessment{PlanID: planID, AssessmentStatus: string(constants.PlanModeled), AlgorithmVersion: s.modelVersion, InputSnapshotJSON: snapshotJSON, CompartmentLoadsJSON: curvesJSON, RiskFlagsJSON: flagsJSON, HighestRiskBand: decompression.HighestRiskBand(result.RiskFlags), ComparativeScore: result.ComparativeScore, AssumptionsJSON: assumptionsJSON}
	actor.Action = "decompression_assessment.run"
	actor.EntityType = "decompression_assessment"
	actor.BeforeSummary = fmt.Sprintf("plan=%d version=%d algorithm=%s segments=%d", planID, plan.Version, s.modelVersion, len(segments))
	actor.AfterSummary = fmt.Sprintf("score=%.2f compartments=%d flags=%d immutable=true", result.ComparativeScore, len(result.Curves), len(result.RiskFlags))
	if err := s.assessments.CreateModeled(ctx, plan, &item, actor); err != nil {
		return dto.AssessmentResponse{}, err
	}
	response, err := dto.DecodeAssessment(item)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	responses := []dto.AssessmentResponse{response}
	if err := s.attachAcks(ctx, responses); err != nil {
		return dto.AssessmentResponse{}, err
	}
	return responses[0], nil
}

func (s *DecompressionAssessmentService) Submit(ctx context.Context, id uint, req dto.TransitionPlanRequest, actor audit.Entry) (dto.AssessmentResponse, error) {
	return s.transition(ctx, id, req.TargetStatus, req.Version, req.Reason, nil, actor)
}

func (s *DecompressionAssessmentService) Approve(ctx context.Context, id uint, req dto.ApproveAssessmentRequest, actor audit.Entry) (dto.AssessmentResponse, error) {
	return s.transition(ctx, id, req.TargetStatus, req.Version, req.Reason, req.Confirmations, actor)
}

func (s *DecompressionAssessmentService) transition(ctx context.Context, id uint, target constants.PlanStatus, version uint, reason string, confirmations []dto.RiskConfirmation, actor audit.Entry) (dto.AssessmentResponse, error) {
	expectedTarget := constants.PlanPendingReview
	if confirmations != nil {
		expectedTarget = constants.PlanApprovedTraining
	}
	if target != expectedTarget {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("endpoint requires target_status %s", expectedTarget), nil)
	}
	// Serialize the read-validate-write decision. With PostgreSQL the optimistic
	// predicates are the real guarantee; under the SQLite smoke store a single
	// writer is permitted, and holding the lock makes a racing duplicate/concurrent
	// confirmation re-read the committed state so it fails with a clean conflict
	// and never inserts a duplicate acknowledgment or audit row.
	unlock := s.assessments.LockTransitions()
	defer unlock()
	assessment, err := s.assessments.Get(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	plan, err := s.plans.Get(ctx, assessment.PlanID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	if plan.Version != version {
		return dto.AssessmentResponse{}, util.Conflict("PLAN_VERSION_CONFLICT", "dive plan was changed by another user", nil)
	}
	if !constants.CanTransitionPlan(plan.PlanStatus, target) {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("cannot transition from %s to %s", plan.PlanStatus, target), nil)
	}
	if assessment.AssessmentStatus != string(plan.PlanStatus) {
		return dto.AssessmentResponse{}, util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment and plan review states do not match", nil)
	}
	var flags []decompression.RiskFlag
	var acks []model.RiskAcknowledgment
	var ackAt time.Time
	if target == constants.PlanApprovedTraining {
		response, decodeErr := dto.DecodeAssessment(assessment)
		if decodeErr != nil {
			return dto.AssessmentResponse{}, util.Internal(decodeErr)
		}
		flags = response.RiskFlags
		if err := validateConfirmations(flags, confirmations); err != nil {
			return dto.AssessmentResponse{}, err
		}
		ackAt = time.Now().UTC()
		acks = make([]model.RiskAcknowledgment, 0, len(confirmations))
		for _, confirmation := range confirmations {
			acks = append(acks, model.RiskAcknowledgment{RiskCode: confirmation.RiskCode, RiskBand: string(confirmation.RiskBand), AckBy: actor.ActorID, AckUsername: actor.ActorUsername, AckAt: ackAt})
		}
	}
	actor.Action = "decompression_assessment.submit_review"
	if target == constants.PlanApprovedTraining {
		actor.Action = "decompression_assessment.approve_training"
	}
	actor.EntityType = "decompression_assessment"
	actor.BeforeSummary = string(plan.PlanStatus)
	actor.AfterSummary = fmt.Sprintf("%s reason=%s human_review=true confirmed_risks=%d", target, strings.TrimSpace(reason), len(acks))
	if err := s.assessments.Transition(ctx, plan, assessment, target, actor.ActorID, ackAt, acks, actor); err != nil {
		return dto.AssessmentResponse{}, err
	}
	// Assemble the read model from data already loaded in this request instead
	// of issuing post-commit reads, keeping the whole decision in one logical
	// unit and avoiding a read-back racing the writer under SQLite.
	response, err := dto.DecodeAssessment(assessment)
	if err != nil {
		return dto.AssessmentResponse{}, util.Internal(err)
	}
	response.AssessmentStatus = string(target)
	if target == constants.PlanApprovedTraining {
		response.RiskAcks = make([]dto.RiskAcknowledgmentResponse, 0, len(acks))
		for _, ack := range acks {
			response.RiskAcks = append(response.RiskAcks, dto.RiskAcknowledgmentResponse{RiskCode: ack.RiskCode, RiskBand: ack.RiskBand, AckBy: ack.AckBy, AckUsername: ack.AckUsername, AckAt: ack.AckAt})
		}
		response.UnconfirmedCount = 0
		reviewedAt := ackAt
		response.ReviewedAt = &reviewedAt
	}
	return response, nil
}

func (s *DecompressionAssessmentService) Compare(ctx context.Context, leftID, rightID uint) (dto.AssessmentComparison, error) {
	left, err := s.Get(ctx, leftID)
	if err != nil {
		return dto.AssessmentComparison{}, err
	}
	right, err := s.Get(ctx, rightID)
	if err != nil {
		return dto.AssessmentComparison{}, err
	}
	return dto.AssessmentComparison{
		Left: left, Right: right, ScoreDelta: right.ComparativeScore - left.ComparativeScore,
		FlagDelta: len(right.RiskFlags) - len(left.RiskFlags),
		Summary: []string{
			fmt.Sprintf("Comparative index changed by %.2f points", right.ComparativeScore-left.ComparativeScore),
			fmt.Sprintf("Risk flag count changed from %d to %d", len(left.RiskFlags), len(right.RiskFlags)),
			"Differences describe deterministic training assumptions, not relative dive safety.",
		}, Disclaimer: dto.SafetyDisclaimer,
	}, nil
}
