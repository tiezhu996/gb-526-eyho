import { useEffect, useMemo, useState } from 'react'
import { Alert, Button, Checkbox, MenuItem, TextField, Tooltip } from '@mui/material'
import { ArrowRight, CheckCheck, GitCompareArrows, ShieldAlert, Send, UserCheck } from 'lucide-react'
import { AssumptionPanel } from '@/components/common/AssumptionPanel'
import { PageHeader } from '@/components/common/PageHeader'
import { PlanStatusBadge } from '@/components/common/PlanStatusBadge'
import { getPlan } from '@/api/plan'
import { useAuth } from '@/hooks/useAuth'
import { useAssessmentPolling } from '@/hooks/useAssessmentPolling'
import { useAssessmentStore } from '@/stores/assessment'
import { usePlanStore } from '@/stores/plan'
import type { RiskBand } from '@/types/risk'

const ACTIONABLE_BANDS: RiskBand[] = ['caution', 'elevated', 'invalid']

const requiresConfirmation = (band: RiskBand) => ACTIONABLE_BANDS.includes(band)

export function AssessmentsPage() {
  const { isPlanner, isSupervisor } = useAuth()
  const plans = usePlanStore()
  const assessments = useAssessmentStore()
  const [compareId, setCompareId] = useState<number>(0)
  const [reason, setReason] = useState('Reviewed training assumptions and versioned model evidence.')
  // riskCode -> whether the supervisor ticked the item for the current review
  const [ticked, setTicked] = useState<Record<string, boolean>>({})
  const [busy, setBusy] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [localError, setLocalError] = useState<string | null>(null)
  useEffect(() => { void assessments.load(); void plans.load() }, [assessments.load, plans.load])
  useAssessmentPolling(true)
  const selected = assessments.selected
  const selectedPlan = useMemo(() => plans.items.find((plan) => plan.id === selected?.plan_id), [plans.items, selected?.plan_id])
  const inSupervisorReview = selected?.assessment_status === 'pending_supervisor_review'
  const actionableFlags = useMemo(() => (selected?.risk_flags ?? []).filter((flag) => requiresConfirmation(flag.band)), [selected?.risk_flags])
  const ackByCode = useMemo(() => new Map((selected?.risk_acks ?? []).map((ack) => [ack.risk_code, ack])), [selected?.risk_acks])
  // Reset local tick state whenever the supervisor opens another immutable run.
  useEffect(() => { setTicked({}) }, [selected?.id])
  const tickedCount = actionableFlags.filter((flag) => ticked[flag.code]).length
  const allTicked = actionableFlags.length > 0 && tickedCount === actionableFlags.length
  const toggleFlag = (code: string) => setTicked((current) => ({ ...current, [code]: !current[code] }))
  const transition = async (kind: 'submit' | 'approve') => {
    if (!selected) return
    setBusy(true); setLocalError(null); setNotice(null)
    try {
      const plan = await getPlan(selected.plan_id)
      if (kind === 'submit') await assessments.submit(selected.id, plan.version, reason)
      else await assessments.approve(selected.id, plan.version, reason, actionableFlags.filter((flag) => ticked[flag.code]).map((flag) => ({ risk_code: flag.code, risk_band: flag.band })))
      await plans.load(); setNotice(kind === 'submit' ? 'Assessment submitted for human supervisor review.' : 'Assessment approved for training comparison; no operational clearance was issued.')
    } catch (error) { setLocalError(error instanceof Error ? error.message : 'Review action failed') }
    finally { setBusy(false) }
  }
  return (
    <div className="page">
      <PageHeader eyebrow="IMMUTABLE MODEL RUNS" title="Assessment review" detail="Compare fixed snapshots, inspect risk evidence, and record explicit human decisions." />
      {(assessments.error || plans.error || localError) && <Alert severity="error">{assessments.error ?? plans.error ?? localError}</Alert>}
      {notice && <Alert severity="success" onClose={() => setNotice(null)}>{notice}</Alert>}
      <div className="assessment-layout">
        <section className="assessment-queue">
          <div className="list-heading"><span>{assessments.items.length} RUNS</span><span>INDEX</span></div>
          {assessments.items.map((item) => <button key={item.id} className={`assessment-row ${selected?.id === item.id ? 'selected' : ''}`} onClick={() => void assessments.select(item.id)}><div><strong>#{item.id} · {plans.items.find((plan) => plan.id === item.plan_id)?.plan_code ?? `Plan ${item.plan_id}`}</strong><span>{item.algorithm_version}</span></div><div><PlanStatusBadge status={item.assessment_status} /><b>{item.comparative_score.toFixed(1)}</b></div></button>)}
          {!assessments.items.length && <div className="empty-state">No immutable assessments recorded.</div>}
        </section>
        <section className="assessment-detail">
          {selected ? <>
            <div className="assessment-title"><div><span className="eyebrow">ASSESSMENT #{selected.id}</span><h2>{selectedPlan?.plan_code ?? `Plan ${selected.plan_id}`}</h2><p>Created {new Date(selected.created_at).toLocaleString()} · input snapshot preserved</p></div><div className="score-dial"><span>COMPARATIVE INDEX</span><strong>{selected.comparative_score.toFixed(1)}</strong><small>{selected.highest_risk_band} · not a safety score</small></div></div>
            <div className="review-bar"><PlanStatusBadge status={selected.assessment_status} /><TextField label="Review reason" value={reason} onChange={(event) => setReason(event.target.value)} fullWidth />{isPlanner && selected.assessment_status === 'modeled' && <Button variant="contained" startIcon={<Send size={17} />} disabled={busy || reason.length < 3} onClick={() => void transition('submit')}>Submit</Button>}{isSupervisor && inSupervisorReview && <Tooltip title={actionableFlags.length === 0 ? 'No caution, elevated or invalid risks require confirmation.' : `Confirm all ${actionableFlags.length} snapshot risks before approving`}><span><Button variant="contained" color="secondary" startIcon={<CheckCheck size={17} />} disabled={busy || reason.length < 3 || (actionableFlags.length > 0 && !allTicked)} onClick={() => void transition('approve')}>Approve training</Button></span></Tooltip>}</div>
            {isSupervisor && inSupervisorReview && actionableFlags.length > 0 && <Alert severity="warning" icon={<UserCheck size={20} />}>Supervisor checklist: {actionableFlags.length - tickedCount} of {actionableFlags.length} caution, elevated or invalid snapshot risks still unconfirmed. Approval is rejected until every item is ticked exactly once.</Alert>}
            <section className="risk-section"><div className="subheading">Risk evidence <span>{selected.risk_flags.length}</span>{actionableFlags.length > 0 && <span className={`ack-counter ${selected.unconfirmed_count === 0 ? 'ack-complete' : ''}`}>{selected.unconfirmed_count} unconfirmed</span>}</div><div className="risk-list">{selected.risk_flags.map((flag) => { const ack = ackByCode.get(flag.code); const actionable = requiresConfirmation(flag.band); return <article className={`risk-row risk-${flag.band}`} key={flag.code}>{inSupervisorReview && isSupervisor && actionable ? <Checkbox size="small" checked={Boolean(ticked[flag.code])} onChange={() => toggleFlag(flag.code)} data-testid={`ack-${flag.code}`} /> : <ShieldAlert size={18} />}<div><strong>{flag.code.replaceAll('_', ' ')}</strong><p>{flag.message}</p><small>{flag.evidence}</small>{ack && <div className="ack-stamp"><UserCheck size={12} /><span>Confirmed by {ack.ack_username} · {new Date(ack.ack_at).toLocaleString()}</span></div>}</div><span>{flag.band}{actionable && !inSupervisorReview && (ack ? ' · confirmed' : '')}</span></article> })}</div></section>
            <div className="compartment-grid">{selected.compartment_loads.map((curve) => { const last = curve.points.at(-1); return <div key={curve.name}><span>{curve.name}</span><strong>{last?.total_inert_bar.toFixed(3)} bar</strong><small>N2 t½ {curve.n2_half_time_min} · He t½ {curve.he_half_time_min}</small></div> })}</div>
            <AssumptionPanel assumptions={selected.assumptions} />
            <section className="compare-panel"><div className="section-title"><GitCompareArrows size={18} /><div><strong>Compare immutable runs</strong><span>Difference is descriptive, not relative safety</span></div></div><TextField select label="Other assessment" value={compareId || ''} onChange={(event) => setCompareId(Number(event.target.value))} sx={{ minWidth: 220 }}>{assessments.items.filter((item) => item.id !== selected.id).map((item) => <MenuItem key={item.id} value={item.id}>#{item.id} · index {item.comparative_score.toFixed(1)}</MenuItem>)}</TextField><Button startIcon={<ArrowRight size={16} />} disabled={!compareId} onClick={() => void assessments.compare(selected.id, compareId)}>Compare</Button>{assessments.comparison && <div className="comparison-result"><strong>{assessments.comparison.score_delta >= 0 ? '+' : ''}{assessments.comparison.score_delta.toFixed(2)} index</strong><span>{assessments.comparison.flag_delta >= 0 ? '+' : ''}{assessments.comparison.flag_delta} flags</span><p>{assessments.comparison.summary.join(' ')}</p></div>}</section>
            <p className="disclaimer-line">{selected.safety_disclaimer}</p>
          </> : <div className="empty-state">Select an assessment to inspect its immutable evidence.</div>}
        </section>
      </div>
    </div>
  )
}
