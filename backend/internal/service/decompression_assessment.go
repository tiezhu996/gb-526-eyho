package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	confirmations, err := s.assessments.ListConfirmationsByAssessments(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	byAssessment := make(map[uint][]model.AssessmentRiskConfirmation, len(items))
	for _, confirmation := range confirmations {
		byAssessment[confirmation.AssessmentID] = append(byAssessment[confirmation.AssessmentID], confirmation)
	}
	responses := make([]dto.AssessmentResponse, 0, len(items))
	for _, item := range items {
		response, decodeErr := dto.DecodeAssessmentWithReview(item, byAssessment[item.ID])
		if decodeErr != nil {
			return nil, 0, decodeErr
		}
		responses = append(responses, response)
	}
	return responses, total, nil
}

func (s *DecompressionAssessmentService) Get(ctx context.Context, id uint) (dto.AssessmentResponse, error) {
	item, err := s.assessments.Get(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	confirmations, err := s.assessments.ListConfirmations(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	return dto.DecodeAssessmentWithReview(item, confirmations)
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
	return dto.DecodeAssessment(item)
}

func (s *DecompressionAssessmentService) Submit(ctx context.Context, id uint, req dto.TransitionPlanRequest, actor audit.Entry) (dto.AssessmentResponse, error) {
	return s.transition(ctx, id, req, constants.PlanPendingReview, actor)
}

// Approve requires the supervisor to confirm every caution, elevated, and
// invalid snapshot risk flag. The submitted set must match the snapshot
// exactly — one extra or one missing code rejects the review before any
// write. Confirmations, the state transition, and audit are committed in a
// single transaction, so a failed approval changes nothing.
func (s *DecompressionAssessmentService) Approve(ctx context.Context, id uint, req dto.TransitionPlanRequest, actor audit.Entry) (dto.AssessmentResponse, error) {
	if req.TargetStatus != constants.PlanApprovedTraining {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("endpoint requires target_status %s", constants.PlanApprovedTraining), nil)
	}
	assessment, err := s.assessments.Get(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	plan, err := s.plans.Get(ctx, assessment.PlanID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	if plan.PlanStatus == constants.PlanApprovedTraining && assessment.AssessmentStatus == string(constants.PlanApprovedTraining) {
		return s.replayApproval(ctx, assessment, req)
	}
	if plan.Version != req.Version {
		return dto.AssessmentResponse{}, util.Conflict("PLAN_VERSION_CONFLICT", "dive plan was changed by another user", nil)
	}
	if !constants.CanTransitionPlan(plan.PlanStatus, constants.PlanApprovedTraining) {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("cannot transition from %s to %s", plan.PlanStatus, constants.PlanApprovedTraining), nil)
	}
	if assessment.AssessmentStatus != string(plan.PlanStatus) {
		return dto.AssessmentResponse{}, util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment and plan review states do not match", nil)
	}
	var flags []decompression.RiskFlag
	if err := json.Unmarshal([]byte(assessment.RiskFlagsJSON), &flags); err != nil {
		return dto.AssessmentResponse{}, util.Internal(fmt.Errorf("decode assessment %d risk flags: %w", assessment.ID, err))
	}
	required := decompression.RequiredConfirmations(flags)
	if err := decompression.ValidateConfirmationSet(required, req.ConfirmedFlags); err != nil {
		return dto.AssessmentResponse{}, util.Unprocessable("RISK_CONFIRMATION_MISMATCH", err.Error(), nil)
	}
	bandByCode := make(map[string]constants.RiskBand, len(flags))
	for _, flag := range flags {
		bandByCode[flag.Code] = flag.Band
	}
	confirmations := make([]model.AssessmentRiskConfirmation, 0, len(required))
	for _, code := range required {
		confirmations = append(confirmations, model.AssessmentRiskConfirmation{AssessmentID: assessment.ID, FlagCode: code, Band: string(bandByCode[code]), ConfirmedBy: actor.ActorID, ConfirmedByName: actor.ActorUsername})
	}
	actor.Action = "decompression_assessment.approve_training"
	actor.EntityType = "decompression_assessment"
	actor.BeforeSummary = string(plan.PlanStatus)
	actor.AfterSummary = fmt.Sprintf("%s reason=%s human_review=true confirmed_flags=%d", constants.PlanApprovedTraining, strings.TrimSpace(req.Reason), len(confirmations))
	if err := s.assessments.ApproveWithConfirmations(ctx, plan, assessment, confirmations, actor.ActorID, actor); err != nil {
		return dto.AssessmentResponse{}, err
	}
	return s.Get(ctx, id)
}

// replayApproval makes a repeated approval idempotent: the same confirmation
// set returns the persisted review state without new writes, while a
// different set is rejected so an approval takes effect exactly once.
func (s *DecompressionAssessmentService) replayApproval(ctx context.Context, assessment model.DecompressionAssessment, req dto.TransitionPlanRequest) (dto.AssessmentResponse, error) {
	confirmations, err := s.assessments.ListConfirmations(ctx, assessment.ID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	recorded := make([]string, 0, len(confirmations))
	for _, confirmation := range confirmations {
		recorded = append(recorded, confirmation.FlagCode)
	}
	sort.Strings(recorded)
	if err := decompression.ValidateConfirmationSet(recorded, req.ConfirmedFlags); err != nil {
		return dto.AssessmentResponse{}, util.Conflict("RISK_CONFIRMATION_CONFLICT", "assessment was already approved with a different confirmation set", nil)
	}
	return s.Get(ctx, assessment.ID)
}

func (s *DecompressionAssessmentService) transition(ctx context.Context, id uint, req dto.TransitionPlanRequest, target constants.PlanStatus, actor audit.Entry) (dto.AssessmentResponse, error) {
	if req.TargetStatus != target {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("endpoint requires target_status %s", target), nil)
	}
	assessment, err := s.assessments.Get(ctx, id)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	plan, err := s.plans.Get(ctx, assessment.PlanID)
	if err != nil {
		return dto.AssessmentResponse{}, err
	}
	if plan.Version != req.Version {
		return dto.AssessmentResponse{}, util.Conflict("PLAN_VERSION_CONFLICT", "dive plan was changed by another user", nil)
	}
	if !constants.CanTransitionPlan(plan.PlanStatus, target) {
		return dto.AssessmentResponse{}, util.Unprocessable("INVALID_PLAN_TRANSITION", fmt.Sprintf("cannot transition from %s to %s", plan.PlanStatus, target), nil)
	}
	if assessment.AssessmentStatus != string(plan.PlanStatus) {
		return dto.AssessmentResponse{}, util.Conflict("ASSESSMENT_STATE_CONFLICT", "assessment and plan review states do not match", nil)
	}
	actor.Action = "decompression_assessment.submit_review"
	if target == constants.PlanApprovedTraining {
		actor.Action = "decompression_assessment.approve_training"
	}
	actor.EntityType = "decompression_assessment"
	actor.BeforeSummary = string(plan.PlanStatus)
	actor.AfterSummary = fmt.Sprintf("%s reason=%s human_review=true", target, strings.TrimSpace(req.Reason))
	if err := s.assessments.Transition(ctx, plan, assessment, target, actor.ActorID, actor); err != nil {
		return dto.AssessmentResponse{}, err
	}
	return s.Get(ctx, id)
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
