import { useEffect, useRef, useState } from 'react'
import { reviewApplication, reviewCustomApplication, launchApplication, type CustomTarget, type LaunchResult, type ReviewResult } from '../services/applications'
import { DuplicatePolicy } from '../../bindings/workspace-manager/internal/domain/models'
import { Modal } from './Modal'

export function ApplicationLaunch({ id = '', customTarget, initialPolicy = DuplicatePolicy.AlwaysLaunch }: { id?: string; customTarget?: CustomTarget; initialPolicy?: DuplicatePolicy }) {
  const [policy, setPolicy] = useState(initialPolicy)
  const [open, setOpen] = useState(false)
  const [review, setReview] = useState<ReviewResult | null>(null)
  const [reviewError, setReviewError] = useState('')
  const [result, setResult] = useState<LaunchResult | null>(null)
  const [unknown, setUnknown] = useState(false)
  const [busy, setBusy] = useState(false)
  const generation = useRef(0)
  useEffect(() => () => { generation.current++ }, [])

  const close = () => { generation.current++; setOpen(false) }
  const prepare = async () => {
    const current = ++generation.current
    setOpen(true); setReview(null); setReviewError(''); setResult(null); setUnknown(false)
    try {
      const next = await (customTarget ? reviewCustomApplication(customTarget, policy) : reviewApplication(id, policy))
      if (current !== generation.current) return
      if (next.code || (!next.entry && !next.custom) || !next.token) setReviewError(next.message || 'Could not review this application. Refresh the catalogue and try again.')
      else setReview(next)
    } catch {
      if (current === generation.current) setReviewError('Could not connect to the desktop service. Retry the review when the connection is available.')
    }
  }
  const launch = async () => {
    if (!review || busy) return
    const current = generation.current
    setBusy(true); setUnknown(false)
    try {
      const next = await launchApplication(review.token)
      if (current === generation.current) setResult(next)
    } catch {
      if (current === generation.current) setUnknown(true)
    } finally {
      if (current === generation.current) setBusy(false)
    }
  }

  const check = unknown || result?.code === 'in_progress'
  return <>
    <label className="launch-policy">Duplicate preference<select aria-label="Launch duplicate preference" value={policy} onChange={event => setPolicy(event.target.value as DuplicatePolicy)}><option value={DuplicatePolicy.SkipIfRunning}>Skip a confirmed running instance</option><option value={DuplicatePolicy.AlwaysLaunch}>Always request a launch</option></select></label>
    <button className="primary-button" onClick={() => void prepare()}>Review launch</button>
    {open && <Modal title="Review application launch" description={customTarget ? 'Check the saved executable and literal arguments before opening this application.' : 'Check the current desktop registration before opening this application.'} busy={busy} onClose={close}>
      {!review && !reviewError && <p role="status">Reading current launch details…</p>}
      {reviewError && <p className="error-panel" role="alert">{reviewError}</p>}
      {review?.entry && <>
        <h3>{review.entry.name}</h3>
        <dl className="launch-review">
          <div><dt>Application ID</dt><dd>{review.entry.id}</dd></div>
          <div><dt>Desktop file</dt><dd>{review.entry.desktopFile}</dd></div>
          <div><dt>Launch method</dt><dd>{review.entry.dbusActivatable ? 'Desktop activation (D-Bus), with desktop fallback' : 'Desktop launch'}</dd></div>
          {review.entry.commandline && <div><dt>{review.entry.dbusActivatable ? 'Fallback launch entry' : 'Launch entry'}</dt><dd>{review.entry.commandline}</dd></div>}
          <div><dt>Working directory</dt><dd>{review.entry.workingDirectory || 'Inherited from Workspace Manager'}</dd></div>
          <div><dt>Terminal window</dt><dd>{review.entry.terminal ? 'Required — uses the desktop terminal configuration' : 'Not required'}</dd></div>
        </dl>
      </>}
      {review?.custom && <><h3>{review.custom.name}</h3><dl className="launch-review"><div><dt>Executable</dt><dd>{review.custom.executable}</dd></div><div><dt>Arguments (one per entry)</dt><dd><ol>{(review.custom.arguments ?? []).map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol>{(review.custom.arguments ?? []).length === 0 && 'No arguments'}</dd></div><div><dt>Working directory</dt><dd>{review.custom.workingDirectory}</dd></div><div><dt>D-Bus identity</dt><dd>{review.custom.instanceBusName || 'Not configured'}</dd></div></dl></>}
      {review && !result && !unknown && <div className="scope-note"><p>{review.instance.message}</p><p>{review.policy === DuplicatePolicy.SkipIfRunning ? 'Skip is allowed only for a confirmed identity. An unknown state blocks launch; select Always request a launch to proceed explicitly.' : 'Launching may open another instance or activate an existing one.'} This review expires in five minutes.</p></div>}
      {result && <p className={['requested', 'in_progress', 'skipped'].includes(result.code) ? 'scope-note' : 'error-panel'} role="status">{result.message}</p>}
      {result?.code === 'instance_unknown' && <label className="launch-policy">Review preference<select aria-label="Review duplicate preference" value={policy} onChange={event => setPolicy(event.target.value as DuplicatePolicy)}><option value={DuplicatePolicy.SkipIfRunning}>Skip a confirmed running instance</option><option value={DuplicatePolicy.AlwaysLaunch}>Always request a launch</option></select></label>}
      {unknown && <p className="error-panel" role="alert">The response was interrupted. The application may have opened. Check the result using the same request before considering another launch.</p>}
      <div className="dialog-actions">
        <button className="secondary-button" data-dialog-initial-focus disabled={busy} onClick={close}>{result || unknown ? 'Close' : 'Cancel'}</button>
        {(reviewError || (result && result.code !== 'in_progress')) && <button className="primary-button" disabled={busy} onClick={() => void prepare()}>Review again</button>}
        {review && (!result || check) && <button className="primary-button" disabled={busy} onClick={() => void launch()}>{busy ? 'Sending launch request…' : check ? 'Check launch result' : 'Confirm launch'}</button>}
      </div>
    </Modal>}
  </>
}
