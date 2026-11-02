import { useEffect, useRef, useState } from 'react'
import { commitSetup, getSetupOutcome, reviewSetup, type SetupResult, type SetupReview } from '../services/projects'
import { ToolVersionChecks } from './ToolVersionChecks'

export function ProjectSetup({ creationToken, templateId, onBusyChange }: { creationToken: string; templateId: string; onBusyChange: (busy: boolean) => void }) {
  const [review, setReview] = useState<SetupReview | null>(null)
  const [results, setResults] = useState<Record<string, SetupResult>>({})
  const [busy, setBusy] = useState(false)
  const [versionsBusy, setVersionsBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const working = useRef(false)
  const request = useRef<AbortController | null>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => () => request.current?.abort(), [])
  useEffect(() => { if (review) heading.current?.focus() }, [review])
  useEffect(() => { onBusyChange(busy || versionsBusy) }, [busy, versionsBusy, onBusyChange])
  useEffect(() => () => onBusyChange(false), [onBusyChange])
  function activity(value: boolean) { working.current = value; setBusy(value) }
  async function prepare(kind: string) {
    if (working.current || uncertain) return
    activity(true); setError('')
    try {
      const result = await reviewSetup(creationToken, kind)
      if (result.review) setReview(result.review)
      else setError(result.message || 'Could not review this setup step.')
    } catch { setError('Could not review setup. No setup command was started.') }
    finally { activity(false) }
  }
  async function run() {
    if (!review || working.current || versionsBusy) return
    activity(true); setError('')
    const controller = new AbortController()
    request.current = controller
    try {
      const result = uncertain ? await getSetupOutcome(review.token) : await commitSetup(review.token, controller.signal)
      if (result.running || result.code === 'setup_pending') { setUncertain(true); setError(result.message); return }
      if (result.code === 'busy') { setError(result.message); return }
      setUncertain(false); setResults(value => ({ ...value, [review.kind]: result })); setReview(null)
      if (!result.succeeded) setError(result.message || 'Setup did not complete. Inspect the project before continuing.')
    } catch { setUncertain(true); setError('The setup response is unavailable. Partial files may remain. Check this same review for its outcome before attempting setup again.') }
    finally { request.current = null; activity(false) }
  }
  const python = templateId === 'python' || templateId === 'fastapi'
  const dependencies = templateId === 'node-typescript' || templateId === 'nextjs' || templateId === 'fastapi'
  return <section aria-label="Optional project setup" className="project-command-editor">
    <h3>Optional project setup</h3>
    <p className="draft-note">Review each step before running it. Setup does not approve or launch startup commands. Keep this window open until setup finishes; saved results are available from the project's Setup history.</p>
    {review ? <div aria-label="Setup command review">
      <h4 ref={heading} tabIndex={-1}>{review.label}</h4>
      <dl className="launch-review"><div><dt>Executable</dt><dd><code>{review.action.executable}</code></dd></div><div><dt>Literal arguments</dt><dd><ol>{review.action.arguments?.map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div><div><dt>Working directory</dt><dd>{review.action.workingDirectory}</dd></div></dl>
      <p className="draft-note">{review.note} {review.network ? 'Network downloads are required.' : 'No network downloads are requested.'} Review expires at {new Date(review.expiresAt).toLocaleTimeString()}.</p>
      <p className="draft-note">Avoid editing project files while setup runs. Cancellation can leave partial output; project files are kept and setup is never retried automatically.</p>
      {!!review.toolChecks?.length && <ToolVersionChecks key={review.token} scope="setup" token={review.token} commands={review.toolChecks} disabled={busy || uncertain} onBusyChange={setVersionsBusy} />}
      <div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy || uncertain || versionsBusy} onClick={() => { setReview(null); setError('') }}>Cancel setup review</button>{busy && !uncertain && <button type="button" className="secondary-button" onClick={() => request.current?.abort()}>Cancel running setup</button>}<button type="button" className="primary-button" disabled={busy || versionsBusy} onClick={() => void run()}>{busy ? uncertain ? 'Checking setup…' : 'Running setup…' : uncertain ? 'Check setup outcome' : 'Run reviewed setup'}</button></div>
    </div> : <div className="dialog-actions">
      <button type="button" className="secondary-button" disabled={busy || !!results.git?.started} onClick={() => void prepare('git')}>Review Git initialization</button>
      {python && <button type="button" className="secondary-button" disabled={busy || !!results.environment?.started} onClick={() => void prepare('environment')}>Review virtual environment</button>}
      {dependencies && <button type="button" className="secondary-button" disabled={busy || !!results.dependencies?.started || (templateId === 'fastapi' && !results.environment?.succeeded)} onClick={() => void prepare('dependencies')}>Review dependency installation</button>}
    </div>}
    {Object.entries(results).map(([kind, result]) => <section key={kind} aria-label={`${kind} setup result`}><p role="status">{result.message}</p>{result.logSaved && <p className="muted text-xs">Result saved in Setup history.</p>}{result.logMessage && <p role="alert" className="error-panel">{result.logMessage}</p>}{result.started && <p className="muted text-xs">Exit code: {result.exitCode}. {result.truncated ? 'Output limited to 64 KiB.' : ''}</p>}{result.output && <pre className="project-setup-output"><code>{result.output}</code></pre>}</section>)}
    {error && <p className="error-panel" role="alert">{error}</p>}
  </section>
}
