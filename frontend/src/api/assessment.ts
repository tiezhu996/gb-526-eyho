import { request } from './client'
import type { Page } from '@/types/common'
import type { AssessmentComparison, DecompressionAssessment, RiskConfirmation } from '@/types/assessment'

export const listAssessments = (planId?: number) => request<Page<DecompressionAssessment>>(`/assessments?size=100${planId ? `&plan_id=${planId}` : ''}`)
export const getAssessment = (id: number) => request<DecompressionAssessment>(`/assessments/${id}`)
export const runAssessment = (planId: number, planVersion: number) => request<DecompressionAssessment>(`/plans/${planId}/assessments/run`, { method: 'POST', body: JSON.stringify({ plan_version: planVersion }) })
export const submitAssessment = (id: number, version: number, reason: string) => request<DecompressionAssessment>(`/assessments/${id}/submit`, { method: 'POST', body: JSON.stringify({ target_status: 'pending_supervisor_review', version, reason }) })
export const approveAssessment = (id: number, version: number, reason: string, confirmations: RiskConfirmation[]) => request<DecompressionAssessment>(`/assessments/${id}/approve`, { method: 'POST', body: JSON.stringify({ target_status: 'approved_for_training', version, reason, confirmations }) })
export const compareAssessments = (leftId: number, rightId: number) => request<AssessmentComparison>(`/assessments/${leftId}/compare?other_id=${rightId}`)
