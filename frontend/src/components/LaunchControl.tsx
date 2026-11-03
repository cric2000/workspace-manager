import { useRef, useState } from 'react'
import { Play, RotateCcw } from 'lucide-react'
import { Modal } from './Modal'
import { CommandRuntimeReview } from './ProjectSettings'
import { execute, type Plan, type Request } from '../services/lifecycle'

export function LaunchControl({ kind, id, name, onAccepted }: { kind: 'project' | 'workspace' | 'session'; id: string; name: string; onAccepted?: () => void }) {
  const [plan, setPlan] = useState<Plan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const working = useRef(false)
  return <>
    <button className="primary-button" data-launch-kind={kind} data-launch-id={id} aria-label={busy ? 'Reviewing…' : kind === 'session' ? `Restore ${name}` : kind === 'project' ? `Run & Preview ${name}` : `Launch ${name}`} disabled={busy} onClick={async () => {
      if (working.current) return
      working.current = true; setBusy(true); setError(''); setNotice('')
      try { const result = await execute({ action: `review_${kind}`, id }); if (result.plan) setPlan(result.plan); else setError(result.message || 'Could not prepare launch review.') }
      catch { setError('Could not connect to the controller. Open the native application and try again.') }
      finally { working.current = false; setBusy(false) }
    }}>{kind === 'session' ? <RotateCcw size={15} aria-hidden="true" /> : <Play size={15} aria-hidden="true" />}{busy ? 'Reviewing…' : kind === 'session' ? 'Restore' : kind === 'project' ? 'Run & Preview' : 'Launch'}</button>
    {error && <p role="alert" className="error-panel">{error}</p>}
    {notice && <p role="status" className="draft-note">{notice}</p>}
    {plan && <LaunchReview plan={plan} onClose={() => setPlan(null)} onAccepted={message => { setPlan(null); setNotice(message); onAccepted?.() }} />}
  </>
}

export function LaunchReview({ plan, onClose, onAccepted }: { plan: Plan; onClose: () => void; onAccepted: (message: string) => void }) {
  const [policy, setPolicy] = useState('keep')
  const [save, setSave] = useState(true)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const working = useRef(false)
  const shutdown = plan.kind === 'review_shutdown'
  const login = plan.kind === 'review_login'
  async function submit(outcome = false) {
    if (working.current) return
    working.current = true; setBusy(true); setError('')
    const request: Request = outcome ? { action: 'outcome', token: plan.token } : { action: 'commit', token: plan.token, switchPolicy: policy, save, name }
    try {
      const result = await execute(request)
      if (result.code) { setError(result.message || 'The operation could not be accepted.'); if (result.code === 'connection_lost') setUncertain(true) }
      else onAccepted(result.message || 'Request accepted. Inspect Activity for progress.')
    } catch { setUncertain(true); setError('The response was lost. Check its outcome before another attempt.') }
    finally { working.current = false; setBusy(false) }
  }
  return <Modal title={shutdown ? 'Save & Shutdown' : login ? 'Review login startup' : `Review ${plan.name}`} description={shutdown ? 'Choose what to save before requesting normal desktop shutdown.' : 'Review the exact configured actions before dispatch. Project code can change independently of its approved command.'} busy={busy} onClose={onClose} wide>
    {plan.warnings?.map((warning, i) => <p key={i} className="draft-note">{warning}</p>)}
    {!plan.tasks?.length && <p className="muted">No configured launch tasks.</p>}
    <ol className="launch-review-list">{plan.tasks?.map(task => <li key={task.key} className="panel">
      <h3>{task.name}</h3><p className="muted text-xs">{task.kind}{task.delaySeconds ? ` · ${task.delaySeconds}s delay` : ''}</p>
      {task.action && <dl className="runtime-command"><dt>Executable</dt><dd><code>{task.action.executable}</code></dd><dt>Arguments</dt><dd><pre>{JSON.stringify(task.action.arguments ?? [])}</pre></dd><dt>Working directory</dt><dd><code>{task.action.workingDirectory}</code></dd></dl>}
      {task.desktopEntry && <dl className="runtime-command"><dt>Desktop registration</dt><dd><code>{task.desktopEntry.desktopFile}</code></dd><dt>Desktop command</dt><dd><code>{task.desktopEntry.commandline}</code></dd><dt>Working directory</dt><dd>{task.desktopEntry.workingDirectory || 'Desktop default'}</dd></dl>}
      {task.application && <p className="muted text-xs">Duplicate policy: {task.application.duplicatePolicy}. Desktop handoffs do not prove window readiness.</p>}
      {task.command && <CommandRuntimeReview runtime={task.command.configuration.runtime} />}
      {task.url && <p className="project-location">{task.url}</p>}
      {task.blocked && <p className="error-panel">Skipped: {task.blocked}</p>}
    </li>)}</ol>
    {!shutdown && !login && <label className="runtime-field">Switch behavior<select aria-label="Switch behavior" disabled={busy || uncertain} value={policy} onChange={e => setPolicy(e.target.value)}><option value="keep">Keep current services running</option><option value="stop_previous">Stop services belonging only to the previous workspace</option></select></label>}
    {shutdown && <fieldset disabled={busy || uncertain} className="editor-fields"><legend>Shutdown choice</legend><label><input type="radio" name="shutdown-save" checked={save} onChange={() => setSave(true)} /> Save session & Shutdown</label><label><input type="radio" name="shutdown-save" checked={!save} onChange={() => setSave(false)} /> Shutdown without saving</label>{save && <label>Session name (optional)<input value={name} onChange={e => setName(e.target.value)} placeholder="Before shutdown" /></label>}</fieldset>}
    {error && <p role="alert" className="error-panel">{error}</p>}
    <div className="dialog-actions"><button className="secondary-button" disabled={busy} onClick={onClose}>Cancel</button>{uncertain ? <button className="primary-button" disabled={busy} onClick={() => void submit(true)}>Check outcome</button> : <button className={shutdown ? 'danger-button' : 'primary-button'} disabled={busy} onClick={() => void submit()}>{busy ? 'Requesting…' : shutdown ? save ? 'Confirm Save & Shutdown' : 'Confirm Shutdown without saving' : login ? 'Enable reviewed login startup' : 'Launch reviewed tasks'}</button>}</div>
  </Modal>
}
