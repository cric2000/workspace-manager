import { useEffect, useRef, useState } from 'react'
import { CommandRuntimeReview, ProjectPreferenceReview } from './ProjectSettings'
import { checkBootstrapRecovery, commitRecoveryRegistration, getRecoveryRegistrationOutcome, reviewRecoveryRegistration, type BootstrapRecoveryReport, type RecoveryRegistrationReview, type RecoveryTarget } from '../services/projects'

export function BootstrapRecovery({ target, disabled, onBusyChange, onRestored }: { target: RecoveryTarget; disabled: boolean; onBusyChange: (busy: boolean) => void; onRestored?: () => void }) {
  const [report, setReport] = useState<BootstrapRecoveryReport | null>(null)
  const [review, setReview] = useState<RecoveryRegistrationReview | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const request = useRef<AbortController | null>(null)
  const working = useRef(false)
  useEffect(() => { onBusyChange(busy) }, [busy,onBusyChange])
  useEffect(() => () => { request.current?.abort(); onBusyChange(false) }, [onBusyChange])
  function activity(value: boolean) { working.current = value; setBusy(value) }
  async function check() {
    if (working.current || disabled) return
    activity(true); setError(''); setNotice(''); setReview(null); setUncertain(false)
    const controller = new AbortController(); request.current = controller
    try { const result = await checkBootstrapRecovery(target,controller.signal); setReport(result.report ?? null); if (result.code) setError(result.message) }
    catch { setError('Recovery check unavailable. Existing files and registrations were kept.') }
    finally { request.current = null; activity(false) }
  }
  async function prepare() {
    if (working.current || disabled) return
    activity(true); setError('')
    try { const result = await reviewRecoveryRegistration(target); if (result.review) setReview(result.review); else setError(result.message || 'Could not review registration recovery. Recheck the destination.') }
    catch { setError('Could not load a recovery review. No registration was requested.') }
    finally { activity(false) }
  }
  async function restore() {
    if (!review || working.current || disabled) return
    activity(true); setError('')
    const controller = new AbortController(); request.current = controller
    try {
      const result = uncertain ? await getRecoveryRegistrationOutcome(review.token) : await commitRecoveryRegistration(review.token,controller.signal)
      if (result.committed) { setNotice(result.message || 'Registration restored. Startup commands remain unapproved.'); setReview(null); setReport(null); setUncertain(false); onRestored?.(); return }
      if (result.code === 'recovery_pending') { setUncertain(true); setError(result.message); return }
      setReview(null); setUncertain(false); setError(result.message || 'Registration was not restored. Recheck recovery.')
    } catch { setUncertain(true); setError('The restore response is unavailable. Check this same review for its outcome or recheck recovery before requesting another restore.') }
    finally { request.current = null; activity(false) }
  }
  return <section aria-label="Bootstrap recovery">
    <div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy || disabled} onClick={() => void check()}>Check recovery</button>{busy && request.current && <button type="button" className="secondary-button" onClick={() => request.current?.abort()}>Cancel recovery request</button>}</div>
    {report && <div role="status"><p>{report.message}</p>{report.assessment && <dl className="launch-review"><div><dt>Destination</dt><dd>{report.assessment.destination}</dd></div><div><dt>Registration</dt><dd>{report.assessment.registration}</dd></div><div><dt>Reviewed files</dt><dd>{report.assessment.files}</dd></div><div><dt>Setup output</dt><dd>{report.assessment.setupOutput}</dd></div></dl>}{report.saved && <p className="muted text-xs">Recovery observations saved. Original task history is unchanged.</p>}</div>}
    {report?.canRestoreRegistration && !review && <button type="button" className="secondary-button" disabled={busy || disabled} onClick={() => void prepare()}>Review registration recovery</button>}
    {review && <div aria-label="Recovered registration review" className="project-command-editor"><h4>{review.project.name}</h4><p className="project-location">{review.project.path}</p><p className="draft-note">Restore only this registration. Files must still match the saved starter. Startup commands remain unapproved, and no setup or project command runs. Review expires at {new Date(review.expiresAt).toLocaleTimeString()}.</p><ProjectPreferenceReview preferences={review.project.preferences} />{review.project.commands?.map(command => <section key={command.id}><h5>{command.configuration.name}</h5><dl className="launch-review"><div><dt>Startup executable · unapproved</dt><dd><code>{command.configuration.executable}</code></dd></div><div><dt>Literal arguments</dt><dd><ol>{command.configuration.arguments?.map((argument,index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div><div><dt>Working directory</dt><dd>{command.configuration.workingDirectory}</dd></div></dl><CommandRuntimeReview runtime={command.configuration.runtime} /></section>)}<div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy || uncertain || disabled} onClick={() => setReview(null)}>Cancel recovery review</button><button type="button" className="primary-button" disabled={busy || disabled} onClick={() => void restore()}>{busy ? uncertain ? 'Checking restore…' : 'Restoring registration…' : uncertain ? 'Check restore outcome' : 'Restore registration'}</button></div></div>}
    {notice && <p role="status">{notice}</p>}
    {error && <p role="alert" className="error-panel">{error}</p>}
  </section>
}
