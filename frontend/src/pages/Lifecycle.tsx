import { useCallback, useEffect, useRef, useState } from 'react'
import { Activity, Power, RefreshCw, Save } from 'lucide-react'
import { ActionMenu } from '../components/ActionMenu'
import { Modal } from '../components/Modal'
import { LaunchControl, LaunchReview } from '../components/LaunchControl'
import { execute, type Response, type Request, type Plan, type SessionView, type Settings } from '../services/lifecycle'

export type LifecyclePageName = 'sessions' | 'activity' | 'settings'
const countLabel = (value: number | undefined, noun: string) => `${value ?? 0} ${noun}${value === 1 ? '' : 's'}`

type SessionAction = { kind: 'capture' | 'branch' | 'rename_session' | 'update_session' | 'delete_session' | 'session_history'; session?: SessionView }

export function Lifecycle({ page, captureRequest = 0, shutdownRequest = 0 }: { page: LifecyclePageName; captureRequest?: number; shutdownRequest?: number }) {
  const [state, setState] = useState<Response | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [plan, setPlan] = useState<Plan | null>(null)
  const [sessionAction, setSessionAction] = useState<SessionAction | null>(null)
  const [force, setForce] = useState<string | null>(null)
  const handledShutdown = useRef(0)
  const handledCapture = useRef(0)
  const alive = useRef(false)
  const working = useRef(false)
  const serial = useRef(0)
  const reload = useCallback(async (quiet = false) => {
    const current = ++serial.current
    if (!quiet) setPending(true)
    try { const result = await execute({ action: 'state' }); if (!alive.current || current !== serial.current) return; if (result.code) setError(result.message); else { setState(result); if (!quiet) setError('') } }
    catch { if (alive.current && current === serial.current) setError('Could not reconnect to the local controller. Reload to try again.') }
    finally { if (alive.current && current === serial.current && !quiet) setPending(false) }
  }, [])
  useEffect(() => { alive.current = true; void reload(); const timer = window.setInterval(() => { if (!working.current && !document.querySelector('dialog[open]')) void reload(true) }, 2000); return () => { alive.current = false; serial.current++; window.clearInterval(timer) } }, [reload])
  useEffect(() => {
    if (captureRequest && handledCapture.current !== captureRequest && state && !pending) {
      handledCapture.current = captureRequest; setSessionAction({ kind: 'capture' })
    }
  }, [captureRequest, state, pending])
  useEffect(() => {
    if (shutdownRequest && handledShutdown.current !== shutdownRequest && state && !pending) {
      handledShutdown.current = shutdownRequest; void run({ action: 'review_shutdown' })
    }
  }, [shutdownRequest, state, pending])
  async function run(request: Request) {
    if (working.current) return
    working.current = true; setPending(true); setError(''); setNotice('')
    try { const result = await execute(request); if (!alive.current) return; if (result.code) setError(result.message); else if (result.plan) setPlan(result.plan); else { setNotice(result.message); await reload(true) } }
    catch { if (alive.current) setError('The controller response was lost. Refresh Activity before retrying a mutation.') }
    finally { working.current = false; if (alive.current) setPending(false) }
  }
  const rt = state?.runtime
  return <>
    <div className="page-heading"><div><p className="eyebrow">YOUR LOCAL WORKSPACE</p><h1>{{ sessions: 'Saved sessions', activity: 'Activity', settings: 'Settings' }[page]}</h1><p className="muted">{{ sessions: 'Save what is open now and restore it when you return.', activity: 'Managed workers keep running when you close this window.', settings: 'Review what starts after login and choose your appearance.' }[page]}</p></div><button className="icon-button" aria-label="Refresh controller state" disabled={pending} onClick={() => void reload()}><RefreshCw size={18} /></button></div>
    {error && <p role="alert" className="error-panel">{error}</p>}{notice && <p role="status" className="draft-note">{notice}</p>}
    {!state && <p className="draft-note">{pending ? 'Connecting to local controller…' : 'Controller unavailable. Refresh to reconnect.'}</p>}
    {state && page === 'sessions' && <><p className="page-explanation">A workspace is your reusable setup. A saved session records the apps and projects captured at one point in time.</p><div className="runtime-actions"><button className="primary-button" onClick={() => setSessionAction({ kind: 'capture' })}><Save size={16} />Save current session</button><button className="secondary-button" disabled={pending || rt?.busy} onClick={() => void run({ action: 'review_shutdown' })}><Power size={16} aria-hidden="true" />Save & Shutdown</button></div><SessionCards sessions={state.sessions ?? []} onAction={setSessionAction} onRestored={() => void reload()} /></>}
    {state && page === 'activity' && <>
      <div className="panel runtime-summary"><Activity size={20} /><div><h2>{rt?.busy ? 'Operation in progress' : 'Controller connected'}</h2><p role="status">{rt?.message || 'Ready for an explicit launch request.'}</p><p className="muted text-xs">Shutdown: {rt?.shutdownState}</p></div>{rt?.busy && <button className="secondary-button" disabled={pending} onClick={() => void run({ action: 'cancel_launch' })}>Cancel current operation</button>}</div>
      {!!rt?.tasks?.length && <ol className="launch-review-list">{rt.tasks.map(task => <li className="panel" key={task.key}><strong>{task.name}</strong><span className={`task-state state-${task.state}`}>{task.state}</span><p className="muted">{task.message}</p></li>)}</ol>}
      <div className="project-library-heading"><h2>Managed command output</h2><button className="secondary-button" disabled={pending} onClick={() => void run({ action: 'clear_finished' })}>Clear finished history</button></div>
      {!rt?.runs?.length && <p className="picker-empty">No managed commands yet. Review a project launch to run one.</p>}
      {rt?.runs?.map(runValue => <article className="panel runtime-run" key={runValue.id}><div className="project-library-heading"><h3>{state.projects?.find(p => p.id === runValue.projectId)?.name || runValue.projectId} · {runValue.command.configuration.name}</h3><span className={`task-state state-${runValue.state}`}>{runValue.state}</span></div><p className="muted">{runValue.message}</p><p className="muted text-xs">Started {new Date(runValue.startedAt).toLocaleString()} · worker {runValue.workerPid || 'registering'}{runValue.state === 'stopped' && runValue.pid > 0 ? runValue.exitCode >= 0 ? ` · exit ${runValue.exitCode}` : ' · terminated by signal' : ''}</p><details><summary>Command configuration</summary><pre className="runtime-output">{runValue.command.configuration.executable}{'\n'}{JSON.stringify(runValue.command.configuration.arguments)}{'\n'}{runValue.command.configuration.workingDirectory}</pre></details><pre className="runtime-output" tabIndex={0} aria-label={`Output for ${runValue.command.configuration.name}`}>{runValue.output || 'No output yet.'}</pre>{runValue.truncated && <p className="muted text-xs">Output is limited to the most recent 64 KiB. Known credentials are redacted.</p>}{runValue.state !== 'stopped' && <div className="runtime-actions"><button className="secondary-button" disabled={pending} onClick={() => void run({ action: 'stop', id: runValue.id })}>Stop gracefully</button><button className="secondary-button delete-button" disabled={pending} onClick={() => setForce(runValue.id)}>Force stop…</button><button className="secondary-button" disabled={pending} onClick={() => void run({ action: 'reconcile_run', id: runValue.id })}>Reconcile finished worker</button></div>}</article>)}
    </>}
    {state?.settings && page === 'settings' && <RuntimeSettings key={state.settingsRevision} settings={state.settings} revision={state.settingsRevision || ''} state={state} onSaved={message => { setNotice(message); void reload() }} />}
    {plan && <LaunchReview plan={plan} onClose={() => setPlan(null)} onAccepted={message => { setPlan(null); setNotice(message); void reload() }} />}
    {sessionAction && <SessionDialog action={sessionAction} onClose={() => setSessionAction(null)} onSaved={message => { setSessionAction(null); setNotice(message); void reload() }} />}
    {force && <Modal title="Force stop managed process?" description="Send SIGKILL only to this worker’s verified process group. Unsaved service state may be lost. Other applications are preserved." busy={pending} onClose={() => setForce(null)}><div className="dialog-actions"><button className="secondary-button" onClick={() => setForce(null)}>Cancel</button><button className="danger-button" disabled={pending} onClick={() => { const id = force; setForce(null); void run({ action: 'stop', id, force: true }) }}>Confirm force stop</button></div></Modal>}
  </>
}

function SessionCards({ sessions, onAction, onRestored }: { sessions: SessionView[]; onAction: (a: SessionAction) => void; onRestored: () => void }) {
  if (!sessions.length) return <div className="panel picker-empty">No snapshots yet. Save a session after launching your workspace.</div>
  return <div className="workspace-grid">{sessions.map(session => <article className="workspace-card" key={session.value.id}><h2>{session.value.name}</h2><p className="muted text-xs">{new Date(session.value.capturedAt).toLocaleString()}</p><p>{countLabel(session.value.projects?.length, 'project')} · {countLabel(session.value.applications?.length, 'recognized application')}</p>{session.value.parentSessionId && <p className="muted text-xs">Copy of {sessions.find(item => item.value.id === session.value.parentSessionId)?.value.name || 'another saved session'}</p>}<details><summary>What will be restored?</summary>{session.value.projects?.map(p => <p key={p.id} className="project-location">{p.name}: {p.path}</p>)}{session.value.applications?.map((app, i) => <p key={i}>{app.custom?.name || app.desktopId}</p>)}{session.value.urls?.map(url => <p key={url} className="project-location">{url}</p>)}{session.value.captureWarnings?.map((warning, i) => <p className="draft-note" key={i}>{warning}</p>)}</details><div className="runtime-actions"><LaunchControl kind="session" id={session.value.id} name={session.value.name} onAccepted={onRestored} /><button className="secondary-button" onClick={() => onAction({ kind: 'branch', session })}>Duplicate</button><ActionMenu name={session.value.name}>{(['rename_session', 'update_session', 'session_history', 'delete_session'] as const).map(kind => <button className={`secondary-button${kind === 'delete_session' ? ' delete-button' : ''}`} key={kind} onClick={() => onAction({ kind, session })}>{{ rename_session: 'Rename', update_session: 'Update from current session…', session_history: 'Version history', delete_session: 'Delete…' }[kind]}</button>)}</ActionMenu></div></article>)}</div>
}

function SessionDialog({ action, onClose, onSaved }: { action: SessionAction; onClose: () => void; onSaved: (message: string) => void }) {
  const [name, setName] = useState(action.kind === 'rename_session' ? action.session?.value.name || '' : action.kind === 'branch' ? `${action.session?.value.name ?? 'Session'} copy` : `Session · ${new Date().toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })}`)
  const [currentState, setCurrentState] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const [history, setHistory] = useState<string[]>([])
  const [revision, setRevision] = useState('')
  const working = useRef(false)
  useEffect(() => { let live = true; if (action.kind === 'session_history') { setBusy(true); execute({ action: 'session_history', id: action.session?.value.id }).then(r => { if (live) { setHistory(r.history ?? []); if (r.code) setError(r.message) } }, () => { if (live) setError('Could not load session history.') }).finally(() => { if (live) setBusy(false) }) } return () => { live = false } }, [action])
  const needsName = ['capture', 'branch', 'rename_session'].includes(action.kind)
  const title = { capture: 'Save current session', branch: 'Duplicate saved session', rename_session: 'Rename session', update_session: 'Update existing snapshot?', delete_session: 'Delete session?', session_history: 'Session revision history' }[action.kind]
  return <Modal title={title} description={action.kind === 'update_session' ? 'Replace this snapshot with current supported state. Its previous bytes remain recoverable in History.' : action.kind === 'delete_session' ? 'Remove this snapshot after checking selected restore targets and ancestry. Project directories are preserved.' : 'Save apps and approved project commands so you can reopen them later. Source files, unsaved documents and browser tab history are not copied.'} busy={busy} onClose={onClose}>
    <form onSubmit={async e => {
      e.preventDefault(); if (working.current || uncertain) return
      working.current = true; setBusy(true); setError('')
      try { const r = await execute({ action: action.kind === 'session_history' ? 'restore_revision' : action.kind, id: action.session?.value.id, revision: action.session?.revision, historyRevision: revision, name, save: currentState }); if (r.code) { setError(r.message); if (r.code === 'connection_lost' || action.kind === 'capture' || action.kind === 'branch') setUncertain(true) } else onSaved(r.message) }
      catch { setUncertain(true); setError('The response was lost. Close this dialog and reload sessions before another mutation.') }
      finally { working.current = false; setBusy(false) }
    }}><div className="editor-fields">{needsName && <label>Session name<input data-dialog-initial-focus value={name} onFocus={e => e.currentTarget.select()} required maxLength={120} disabled={busy || uncertain} onChange={e => setName(e.target.value)} /></label>}{action.kind === 'branch' && <label><input type="checkbox" checked={currentState} onChange={e => setCurrentState(e.target.checked)} disabled={busy || uncertain} /> Capture current supported state into the branch</label>}{action.kind === 'session_history' && <label>Revision to restore<select value={revision} onChange={e => setRevision(e.target.value)} disabled={busy || uncertain}><option value="">Choose an archived revision</option>{history.map(r => <option key={r} value={r}>{r.slice(0, 16)}…</option>)}</select>{!history.length && <p className="muted">No archived revisions.</p>}</label>}</div>{error && <p role="alert" className="error-panel">{error}</p>}<div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onClose}>Cancel</button><button type="submit" className={action.kind === 'delete_session' ? 'danger-button' : 'primary-button'} disabled={busy || uncertain || (action.kind === 'session_history' && !revision)}>{busy ? 'Saving…' : action.kind === 'delete_session' ? 'Confirm delete session' : action.kind === 'update_session' ? 'Confirm update snapshot' : action.kind === 'session_history' ? 'Restore selected revision' : 'Save session'}</button></div></form>
  </Modal>
}

function RuntimeSettings({ settings, revision, state, onSaved }: { settings: Settings; revision: string; state: Response; onSaved: (message: string) => void }) {
  const [draft, setDraft] = useState<Settings>({ ...settings })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [plan, setPlan] = useState<Plan | null>(null)
  const working = useRef(false)
  function appearance() { localStorage.setItem('workspace-manager-appearance', draft.appearance); window.dispatchEvent(new Event('workspace-manager-appearance')) }
  return <form className="panel runtime-settings" onSubmit={async e => { e.preventDefault(); if (working.current) return; working.current = true; setBusy(true); setError(''); try { const r = await execute({ action: draft.autostart ? 'review_login' : 'save_settings', settings: draft, revision }); if (r.code) setError(r.message); else if (r.plan) setPlan(r.plan); else { appearance(); onSaved(r.message) } } catch { setError('Settings response was lost. Reload Settings before trying again.') } finally { working.current = false; setBusy(false) } }}>
    <div className="editor-fields"><label>After login, open<select disabled={busy} value={draft.startupMode} onChange={e => setDraft({ ...draft, startupMode: e.target.value as Settings['startupMode'] })}><option value="fixed">A workspace</option><option value="restore">A saved session</option><option value="hybrid">Workspace and saved session</option></select></label><label hidden={draft.startupMode === 'restore'}>Default workspace<select disabled={busy} value={draft.defaultWorkspaceId || ''} onChange={e => setDraft({ ...draft, defaultWorkspaceId: e.target.value })}><option value="">Choose a workspace</option>{state.workspaces?.map(w => <option key={w.id} value={w.id}>{w.name}</option>)}</select></label><label hidden={draft.startupMode === 'fixed'}>Restore session<select disabled={busy} value={draft.restoreSessionId || ''} onChange={e => setDraft({ ...draft, restoreSessionId: e.target.value })}><option value="">Choose a snapshot</option>{state.sessions?.map(s => <option key={s.value.id} value={s.value.id}>{s.value.name}</option>)}</select></label><label>Initial login delay (seconds)<input type="number" min={0} max={3600} disabled={busy} value={draft.initialDelaySeconds} onChange={e => setDraft({ ...draft, initialDelaySeconds: Number(e.target.value) })} /></label><label>Appearance<select disabled={busy} value={draft.appearance} onChange={e => setDraft({ ...draft, appearance: e.target.value as Settings['appearance'] })}><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select></label><label><input type="checkbox" checked={draft.autostart} disabled={busy} onChange={e => setDraft({ ...draft, autostart: e.target.checked })} /> Start the reviewed configuration after login</label></div><p className="draft-note">Enabling login startup requires a review of its exact actions. Changes to commands, editor paths, applications or startup targets require another review. Browser tabs and unsaved editor documents use each application's own recovery support.</p>{error && <p role="alert" className="error-panel">{error}</p>}<div className="dialog-actions"><button className="primary-button" disabled={busy}>{busy ? 'Checking…' : draft.autostart ? 'Review & enable login startup' : 'Save settings'}</button></div>{plan && <LaunchReview plan={plan} onClose={() => setPlan(null)} onAccepted={message => { appearance(); setPlan(null); onSaved(message) }} />}
  </form>
}
