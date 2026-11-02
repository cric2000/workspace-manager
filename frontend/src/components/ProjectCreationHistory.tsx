import { useEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { listCreationLogs, type CreationLog } from '../services/projects'
import { BootstrapRecovery } from './BootstrapRecovery'

export function ProjectCreationHistory({ onClose, onRestored }: { onClose: () => void; onRestored?: () => void }) {
  const [logs, setLogs] = useState<CreationLog[] | null>(null)
  const [next, setNext] = useState('')
  const [page, setPage] = useState({ cursor: '', previous: [] as string[] })
  const [attempt, setAttempt] = useState(0)
  const [busy, setBusy] = useState(false)
  const [recovering, setRecovering] = useState(false)
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    request.current = controller
    setBusy(true); setError(''); setLogs(null); setNext('')
    listCreationLogs(page.cursor, controller.signal).then(result => {
      if (request.current !== controller) return
      if (result.code) setError(result.message || 'Could not read creation history.')
      else { setLogs(result.logs ?? []); setNext(result.next) }
    }, () => { if (request.current === controller) setError('Could not load creation history. Reload to try again.') }).finally(() => { if (request.current === controller) { request.current = null; setBusy(false) } })
    return () => { if (request.current === controller) request.current = null; controller.abort() }
  }, [page.cursor, attempt])
  return <Modal title="Creation history" description="Saved creation and official scaffolding attempts, including projects that were never registered. Reading history runs no commands." busy={recovering} onClose={onClose} wide>
    <p className="draft-note">Newest attempts first, 20 per page. Missing final results do not confirm success or whether an operation is still running. Inspect the destination before creating again; history never resumes or retries an operation.</p>
    {busy && <p role="status">Loading creation history…</p>}
    {error && <p className="error-panel" role="alert">{error}</p>}
    {logs?.length === 0 && <p role="status">No saved creation attempts on this page.</p>}
    {logs?.map(log => <section key={log.id} aria-label={`${log.name} ${log.kind} history`} className="project-command-editor">
      <h3>{log.name} · {log.kind === 'preparation' ? 'Official scaffolding' : 'File publication'}</h3>
      <p className="project-location">{log.path}</p>
      <p className="muted text-xs">{log.templateId} · Recorded {new Date(log.recordedAt).toLocaleString()}{log.finishedAt ? ` · Finished ${new Date(log.finishedAt).toLocaleString()}` : ''}</p>
      {log.result ? <>
        <p>{log.result.succeeded ? 'Succeeded.' : 'Needs attention.'} {log.result.message}</p>
        <p className="draft-note">{log.kind === 'publication' ? `${log.result.fileCount} files in the reviewed plan. Files published: ${log.result.created ? 'yes' : 'no'}. Registration saved: ${log.result.registered ? 'yes' : 'no'}.` : `${log.result.fileCount} files captured. Tool process started: ${log.result.processStarted ? 'yes' : 'no'}. Exit code: ${log.result.exitCode}.`}</p>
        {log.result.recoveryPath && <p className="project-location">Recovery path: {log.result.recoveryPath}</p>}
        {!!log.result.activity?.length && <ol>{log.result.activity.map((line,index) => <li key={index}>{line}</li>)}</ol>}
        {log.result.truncated && <p className="muted text-xs">Output limited to 64 KiB.</p>}
        {log.result.output && <pre className="project-setup-output"><code>{log.result.output}</code></pre>}
      </> : <p className="draft-note">No final outcome was saved. Check the destination and registered projects; this record does not confirm whether preparation or publication finished.</p>}
      <BootstrapRecovery target={{ scope: 'creation', projectId: log.projectId, logId: log.id }} disabled={recovering} onBusyChange={setRecovering} onRestored={onRestored} />
    </section>)}
    <div className="dialog-actions">
      <button type="button" className="secondary-button" disabled={busy || recovering} onClick={() => { setPage({ cursor: '', previous: [] }); setAttempt(value => value + 1) }}>Reload newest history</button>
      <button type="button" className="secondary-button" disabled={busy || recovering || !page.previous.length} onClick={() => setPage({ cursor: page.previous[page.previous.length - 1], previous: page.previous.slice(0,-1) })}>Newer attempts</button>
      <button type="button" className="secondary-button" disabled={busy || recovering || !next} onClick={() => setPage({ cursor: next, previous: [...page.previous, page.cursor] })}>Older attempts</button>
      <button type="button" className="primary-button" disabled={recovering} data-dialog-initial-focus onClick={onClose}>Done</button>
    </div>
  </Modal>
}
