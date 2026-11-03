import { useEffect, useRef, useState, type ReactNode } from 'react'
import { AppWindow, Copy, Folder, Layers3, Pencil, Plus, RefreshCw, Search, Star, Trash2 } from 'lucide-react'
import { Modal } from '../components/Modal'
import { WorkspaceProjects } from '../components/WorkspaceProjects'
import { WorkspaceApplications } from '../components/WorkspaceApplications'
import { LaunchControl } from '../components/LaunchControl'
import { createWorkspace, deleteWorkspace, duplicateWorkspace, editWorkspace, listWorkspaces, setDefaultWorkspace, setWorkspaceApplications, setWorkspaceProjects, type Draft, type Result, type State, type View } from '../services/workspaces'

type ProfileEditor = { kind: 'create' } | { kind: 'edit' | 'duplicate' | 'delete'; workspace: View }
type Editor = ProfileEditor | { kind: 'applications'; workspace: View } | { kind: 'projects'; workspace: View }
type Fault = { message: string; reconcile: boolean }
const target = (workspace: View) => ({ id: workspace.id, revision: workspace.revision })

export function Workspaces({ createRequest = 0 }: { createRequest?: number }) {
  const [state, setState] = useState<State | null>(null)
  const [pending, setPending] = useState(true)
  const [fault, setFault] = useState<Fault | null>(null)
  const [notice, setNotice] = useState('')
  const [editor, setEditor] = useState<Editor | null>(null)
  const [blockedDraft, setBlockedDraft] = useState(false)
  const [needsReload, setNeedsReload] = useState(false)
  const [query, setQuery] = useState('')
  const handledCreate = useRef(0)
  const busy = useRef(false)
  const mounted = useRef(false)

  useEffect(() => {
    mounted.current = true
    let current = true
    listWorkspaces().then(result => {
      if (!current) return
      if (result.code || !result.state) setFault({ message: result.message || 'Could not load workspaces.', reconcile: true })
      else setState(result.state)
      setPending(false)
    }, () => {
      if (!current) return
      setFault({ message: 'Could not connect to local storage. Open the native application and reload workspaces.', reconcile: true })
      setPending(false)
    })
    return () => { current = false; mounted.current = false }
  }, [])

  useEffect(() => {
    if (createRequest && handledCreate.current !== createRequest && state && !pending && !needsReload) {
      handledCreate.current = createRequest
      setEditor({ kind: 'create' }); setFault(null); setNotice(''); setBlockedDraft(false)
    }
  }, [createRequest, state, pending, needsReload])

  async function run(operation: () => Promise<Result>, success: string, mutation = true) {
    if (busy.current) return
    busy.current = true
    setPending(true)
    setFault(null)
    setNotice('')
    try {
      const result = await operation()
      if (!mounted.current) return
      if (result.code || !result.state) {
        const reconcile = result.committed || !['validation', 'default_workspace', 'default_required', 'application_unavailable', 'project_unavailable'].includes(result.code)
        setFault({ message: result.message || 'Could not load workspaces. Reload before continuing.', reconcile })
        if (reconcile) { setNeedsReload(true); if (mutation) setBlockedDraft(true) }
        if (result.committed) setEditor(null)
        return
      }
      setState(result.state)
      setNeedsReload(false)
      setNotice(success)
      if (mutation) setEditor(null)
    } catch {
      if (!mounted.current) return
      // A lost response may follow a successful write: reconcile before retrying.
      setFault({ message: mutation ? 'The desktop connection was interrupted. The change may have been saved. Reload workspaces, then review the saved state before trying again.' : 'Could not connect to local storage. Open the native application and reload workspaces.', reconcile: true })
      setNeedsReload(true)
      if (mutation) setBlockedDraft(true)
    } finally {
      busy.current = false
      if (mounted.current) setPending(false)
    }
  }

  function openEditor(next: Editor) {
    setFault(null)
    setNotice('')
    setBlockedDraft(false)
    setEditor(next)
  }

  function submit(draft: Draft) {
    if (!editor || blockedDraft || needsReload) return
    switch (editor.kind) {
      case 'create': void run(() => createWorkspace(draft), 'Workspace created.'); break
      case 'edit': void run(() => editWorkspace(target(editor.workspace), draft), 'Workspace saved.'); break
      case 'duplicate': void run(() => duplicateWorkspace(target(editor.workspace), draft), 'Workspace duplicated.'); break
      case 'delete': void run(() => deleteWorkspace(target(editor.workspace)), 'Workspace deleted. Project files are unchanged.'); break
    }
  }

  const reload = () => { void run(listWorkspaces, 'Workspaces reloaded.', false) }
  const disabled = pending || !state || needsReload
  const workspaces = state?.workspaces ?? []
  const filtered = workspaces.filter(workspace => `${workspace.name} ${workspace.description}`.toLocaleLowerCase().includes(query.toLocaleLowerCase()))
  const defaultWorkspace = workspaces.find(workspace => workspace.id === state?.defaultWorkspaceId)
  const errorPanel = fault && <div className="error-panel" role="alert"><p>{fault.message}</p>{fault.reconcile && <button className="secondary-button" disabled={pending} onClick={reload}>Reload workspaces</button>}</div>

  return <>
    <section className="workspace-heading" aria-labelledby="workspaces-title">
      <div><p className="eyebrow">YOUR REUSABLE ENVIRONMENTS</p><h1 id="workspaces-title">Workspaces</h1><p className="muted">Give each kind of work a place of its own.</p></div>
      <button data-new-workspace className="primary-button" disabled={disabled} onClick={() => openEditor({ kind: 'create' })}><Plus size={17} aria-hidden="true" />New workspace</button>
    </section>
    <section className="default-summary" aria-label="Default workspace">
      <span className="summary-icon"><Star size={19} aria-hidden="true" /></span>
      <div><p className="eyebrow">DEFAULT WORKSPACE</p><p className="default-name">{state ? state.defaultMissing ? 'Saved default is unavailable' : defaultWorkspace?.name || 'No default selected' : 'Loading your preferences…'}</p><p className="muted text-xs">{state?.defaultMissing ? 'Clear this reference or choose an existing workspace.' : 'A saved preference for future startup. Login startup is not enabled by this action.'}</p></div>
      {state?.defaultWorkspaceId && <button className="secondary-button" disabled={disabled} onClick={() => { void run(() => setDefaultWorkspace({ id: '', revision: '' }, state.settingsRevision), 'Default cleared.') }}>Clear default</button>}
    </section>
    {!editor && errorPanel}
    <p role="status" className="operation-status" aria-live="polite">{pending ? state ? 'Updating workspaces…' : 'Loading workspaces…' : notice}</p>
    {state && <>
      <div className="workspace-toolbar">
        <p className="muted text-xs">{workspaces.length} {workspaces.length === 1 ? 'workspace' : 'workspaces'}</p>
        <div className="flex items-center gap-3"><label className="search-field"><Search size={15} aria-hidden="true" /><input type="search" aria-label="Search workspaces" placeholder="Find a workspace…" value={query} onChange={event => setQuery(event.target.value)} /></label><button className="icon-button" aria-label="Reload workspaces" title="Reload workspaces" disabled={pending} onClick={reload}><RefreshCw size={16} aria-hidden="true" /></button></div>
      </div>
      {filtered.length ? <div className="workspace-grid" aria-busy={pending}>
        {filtered.map(workspace => <article className="workspace-card" key={workspace.id} aria-label={workspace.name}>
          <div className="card-top"><span className="workspace-icon"><Layers3 size={21} aria-hidden="true" /></span>{workspace.id === state.defaultWorkspaceId && <span className="default-badge"><Star size={11} aria-hidden="true" />Default</span>}</div>
          <h2>{workspace.name}</h2><p className={`workspace-description ${workspace.description ? '' : 'muted'}`}>{workspace.description || 'Add a description to make this workspace your own.'}</p>
          <div className="workspace-meta"><span><Folder size={14} aria-hidden="true" />{workspace.projectCount} project {workspace.projectCount === 1 ? 'reference' : 'references'}</span><span>Edited {new Date(workspace.updatedAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}</span></div>
          <div className="card-actions">
            <LaunchControl kind="workspace" id={workspace.id} name={workspace.name} />
            <button className="secondary-button" aria-label={`Edit ${workspace.name}`} disabled={disabled} onClick={() => openEditor({ kind: 'edit', workspace })}><Pencil size={13} aria-hidden="true" />Edit</button>
            <button className="secondary-button" aria-label={`Configure applications for ${workspace.name}`} disabled={disabled} onClick={() => openEditor({ kind: 'applications', workspace })}><AppWindow size={13} aria-hidden="true" />Apps ({workspace.applications?.length ?? 0})</button>
            <button className="secondary-button" aria-label={`Configure projects for ${workspace.name}`} disabled={disabled} onClick={() => openEditor({ kind: 'projects', workspace })}><Folder size={13} aria-hidden="true" />Projects ({workspace.projectCount})</button>
            <button className="icon-button" aria-label={`Duplicate ${workspace.name}`} title="Duplicate workspace" disabled={disabled} onClick={() => openEditor({ kind: 'duplicate', workspace })}><Copy size={15} aria-hidden="true" /></button>
            {workspace.id !== state.defaultWorkspaceId && <button className="icon-button" aria-label={`Set ${workspace.name} as default`} title="Set as default" disabled={disabled} onClick={() => { void run(() => setDefaultWorkspace(target(workspace), state.settingsRevision), 'Default workspace updated.') }}><Star size={15} aria-hidden="true" /></button>}
            <button className="icon-button delete-button" aria-label={`Delete ${workspace.name}`} title="Delete workspace" disabled={disabled} onClick={() => openEditor({ kind: 'delete', workspace })}><Trash2 size={15} aria-hidden="true" /></button>
          </div>
        </article>)}
      </div> : <div className="empty-workspaces panel"><span className="workspace-icon"><Layers3 size={27} aria-hidden="true" /></span><h2>{query ? 'No matching workspaces' : 'Start with a space for your work'}</h2><p className="muted">{query ? 'Try another name or description.' : 'Create a profile for coding, learning or your next idea.'}</p>{!query && <button className="secondary-button" disabled={disabled} onClick={() => openEditor({ kind: 'create' })}><Plus size={15} aria-hidden="true" />Create your first workspace</button>}</div>}
    </>}
    {!state && !pending && <button className="secondary-button" onClick={reload}>Retry loading</button>}
    <aside className="scope-note"><h2>A reusable setup for your work.</h2><p>Arrange applications and projects here. Launch reviews show exact commands and skip unavailable or unapproved tasks. Configure URLs, editor and delays from Dashboard; inspect managed workers in Activity.</p></aside>
    {editor?.kind === 'applications' ? <WorkspaceApplications workspace={editor.workspace} pending={pending} blocked={blockedDraft} error={errorPanel} onClose={() => setEditor(null)} onSave={choices => { if (!blockedDraft && !needsReload) void run(() => setWorkspaceApplications(target(editor.workspace), choices), 'Workspace applications saved.') }} /> : editor?.kind === 'projects' ? <WorkspaceProjects workspace={editor.workspace} pending={pending} blocked={blockedDraft} error={errorPanel} onClose={() => setEditor(null)} onSave={choices => { if (!blockedDraft && !needsReload) void run(() => setWorkspaceProjects(target(editor.workspace), choices), 'Workspace projects saved.') }} /> : editor && <WorkspaceEditor key={editor.kind + ('workspace' in editor ? editor.workspace.id : '')} editor={editor} pending={pending} blocked={blockedDraft} error={errorPanel} onClose={() => setEditor(null)} onSubmit={submit} />}
  </>
}

function WorkspaceEditor({ editor, pending, blocked, error, onClose, onSubmit }: {
  editor: ProfileEditor; pending: boolean; blocked: boolean; error: ReactNode; onClose: () => void; onSubmit: (draft: Draft) => void
}) {
  const [name, setName] = useState('workspace' in editor ? editor.workspace.name : '')
  const [description, setDescription] = useState('workspace' in editor ? editor.workspace.description : '')
  const [validation, setValidation] = useState('')
  const title = { create: 'New workspace', edit: 'Edit workspace', duplicate: 'Duplicate workspace', delete: 'Delete workspace' }[editor.kind]
  const action = { create: 'Create workspace', edit: 'Save changes', duplicate: 'Create duplicate', delete: 'Delete workspace' }[editor.kind]
  const hint = editor.kind === 'delete' ? `Delete “${'workspace' in editor ? editor.workspace.name : ''}”? Project directories and their files will be kept.` : editor.kind === 'duplicate' ? 'Give the copy a name. Project references are shared; project directories are not copied.' : 'Save a reusable profile. You can change these details later.'

  return <Modal title={title} description={hint} busy={pending} onClose={onClose}>
    <form onSubmit={event => {
      event.preventDefault()
      if (pending || blocked) return
      if (editor.kind !== 'delete' && (!name.trim() || Array.from(name.trim()).length > 120 || /[\u0000-\u001f\u007f-\u009f]/.test(name) || Array.from(description).length > 4096 || description.includes('\0'))) {
        setValidation('Use a name of 1–120 characters without control characters and a description of at most 4,096 characters.'); return
      }
      setValidation('')
      onSubmit({ name: name.trim(), description })
    }}>
      {editor.kind !== 'delete' && <div className="editor-fields">
        <label htmlFor="workspace-name">Name<input id="workspace-name" name="name" data-dialog-initial-focus required disabled={pending} value={name} onChange={event => setName(event.target.value)} placeholder="e.g. Coding" /></label>
        <label htmlFor="workspace-description">Description <span className="muted font-normal">(optional)</span><textarea id="workspace-description" name="description" rows={4} disabled={pending} value={description} onChange={event => setDescription(event.target.value)} placeholder="What will you work on here?" /></label>
        <p className="muted text-xs">{Array.from(description).length.toLocaleString()} / 4,096 characters</p>
      </div>}
      {validation && <p className="error-panel" role="alert">{validation}</p>}{error}
      {blocked && <p className="draft-note">Your draft is kept here for reference. After reloading, close this dialog and reopen the workspace to review its latest version.</p>}
      <div className="dialog-actions"><button type="button" className="secondary-button" disabled={pending} onClick={onClose} data-dialog-initial-focus={editor.kind === 'delete' ? true : undefined}>Cancel</button><button type="submit" className={editor.kind === 'delete' ? 'danger-button' : 'primary-button'} disabled={pending || blocked}>{pending ? 'Saving…' : action}</button></div>
    </form>
  </Modal>
}
