import { useEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { execute, type Workspace } from '../services/lifecycle'

export function WorkspaceLaunchPreferences({ id, name, onClose, onSaved }: { id: string; name: string; onClose: () => void; onSaved: (message: string) => void }) {
  const [selected, setSelected] = useState<{ workspace: Workspace; revision: string } | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true
    execute({ action: 'state' }).then(state => {
      if (!live) return
      const workspace = state.workspaces?.find(item => item.id === id), revision = state.workspaceRevisions?.[id]
      if (state.code || !workspace || !revision) setError(state.message || 'Workspace settings are unavailable. Refresh and try again.')
      else setSelected({ workspace, revision })
    }, () => { if (live) setError('Could not load launch preferences. Refresh and try again.') })
    return () => { live = false }
  }, [id])
  return selected ? <WorkspaceLaunchDialog workspace={selected.workspace} revision={selected.revision} onClose={onClose} onSaved={message => { onSaved(message); onClose() }} /> : <Modal title={`Launch preferences · ${name}`} description={error || 'Loading your saved URLs, editor and delay settings…'} busy={false} onClose={onClose}><button className="secondary-button" onClick={onClose}>Close</button></Modal>
}

function WorkspaceLaunchDialog({ workspace, revision, onClose, onSaved }: { workspace: Workspace; revision: string; onClose: () => void; onSaved: (message: string) => void }) {
  const [urls, setURLs] = useState(workspace.launch?.urls?.join('\n') ?? '')
  const [delay, setDelay] = useState(workspace.launch?.delaySeconds ?? 0)
  const [editor, setEditor] = useState(workspace.launch?.defaultEditor?.executable ?? '')
  const [args, setArgs] = useState(JSON.stringify(workspace.launch?.defaultEditor?.arguments ?? []))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const working = useRef(false)
  return <Modal title={`Launch preferences · ${workspace.name}`} description="Application and project order follows your workspace configuration. Each editor invocation adds the project directory as one literal argument. Review all actions before launching." busy={busy} onClose={onClose}><form onSubmit={async e => { e.preventDefault(); if (working.current) return; working.current = true; setBusy(true); setError(''); try { const arguments_: unknown = JSON.parse(args); if (!Array.isArray(arguments_) || arguments_.some(v => typeof v !== 'string')) throw new Error('arguments'); const result = await execute({ action: 'configure_workspace', id: workspace.id, revision, workspaceLaunch: { urls: urls.split('\n').map(v => v.trim()).filter(Boolean), delaySeconds: delay, defaultEditor: editor ? { executable: editor, arguments: arguments_, workingDirectory: workspace.launch?.defaultEditor?.workingDirectory || '/' } : undefined } }); if (result.code) setError(result.message); else onSaved(result.message) } catch { setError('Use a JSON array of literal editor arguments and an available workspace. Reload before retrying after a lost response.') } finally { working.current = false; setBusy(false) } }}><div className="editor-fields"><label>Browser URLs (one HTTP(S) URL per line)<textarea data-dialog-initial-focus rows={4} value={urls} onChange={e => setURLs(e.target.value)} disabled={busy} /></label><label>Delay between tasks (seconds)<input type="number" min={0} max={3600} value={delay} onChange={e => setDelay(Number(e.target.value))} disabled={busy} /></label><label>Default editor executable (optional absolute path)<input value={editor} onChange={e => setEditor(e.target.value)} disabled={busy} placeholder="/usr/bin/code" /></label><label>Editor arguments (JSON array)<input value={args} onChange={e => setArgs(e.target.value)} disabled={busy} /></label></div>{error && <p role="alert" className="error-panel">{error}</p>}<div className="dialog-actions"><button type="button" className="secondary-button" onClick={onClose} disabled={busy}>Cancel</button><button className="primary-button" disabled={busy}>Save launch preferences</button></div></form></Modal>
}
