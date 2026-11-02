import { useEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { listSetupLogs, type ProjectView, type SetupLog } from '../services/projects'
import { BootstrapRecovery } from './BootstrapRecovery'

export function ProjectSetupHistory({ view, onClose }: { view: ProjectView; onClose: () => void }) {
  const [logs, setLogs] = useState<SetupLog[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [recovering, setRecovering] = useState(false)
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)
  const alive = useRef(true)
  async function load() {
    if (request.current) return
    const controller = new AbortController()
    request.current = controller
    setBusy(true); setError(''); setLogs(null)
    try {
      const result = await listSetupLogs(view.project.id, controller.signal)
      if (!alive.current || request.current !== controller) return
      if (result.code) setError(result.message || 'Could not load setup history.')
      else setLogs(result.logs ?? [])
    } catch { if (alive.current && request.current === controller) setError('Could not load setup history. Close this window or reload to try again.') }
    finally { if (alive.current && request.current === controller) { request.current = null; setBusy(false) } }
  }
  useEffect(() => { alive.current = true; void load(); return () => { alive.current = false; request.current?.abort(); request.current = null } }, [view.project.id])
  return <Modal title={`Setup history · ${view.project.name}`} description="Saved setup attempts and output. Reading history never starts, resumes or retries a command." busy={recovering} onClose={onClose} wide>
    <p className="project-location">{view.project.path}</p>
    <p className="draft-note">Results are stored locally and remain available after restart. Reviews and execution approval are separate. If a final outcome is missing, setup may still be running elsewhere or may have stopped; inspect the project before continuing.</p>
    {busy && <p role="status">Loading setup history…</p>}
    {error && <p className="error-panel" role="alert">{error}</p>}
    {logs?.length === 0 && <p role="status">No saved setup attempts for this project.</p>}
    {logs?.map(log => <section className="project-command-editor" aria-label={`${log.label} history`} key={log.id}>
      <h3>{log.label}</h3>
      <p className="muted text-xs">Recorded {new Date(log.recordedAt).toLocaleString()}{log.finishedAt ? ` · Finished ${new Date(log.finishedAt).toLocaleString()}` : ''}</p>
      {log.result ? <>
        <p>{log.result.succeeded ? 'Succeeded.' : log.result.started ? 'Did not complete successfully; partial files may remain.' : 'No setup process started.'} {log.result.message}</p>
        <p className="muted text-xs">Exit code: {log.result.exitCode}. {log.result.truncated ? 'Output limited to 64 KiB.' : ''}</p>
        {log.result.output && <pre className="project-setup-output"><code>{log.result.output}</code></pre>}
      </> : <p className="draft-note">No final outcome was saved. This record does not confirm whether a process started or finished. Setup will not retry automatically.</p>}
      <BootstrapRecovery target={{ scope: 'setup', projectId: log.projectId, logId: log.id }} disabled={recovering} onBusyChange={setRecovering} />
    </section>)}
    <div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy || recovering} onClick={() => void load()}>Reload setup history</button><button type="button" className="primary-button" disabled={recovering} data-dialog-initial-focus onClick={onClose}>Done</button></div>
  </Modal>
}
