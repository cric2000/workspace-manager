import { useEffect, useState, type ReactNode } from 'react'
import { AppWindow, ArrowDown, ArrowUp, Plus, RefreshCw, Search, Trash2 } from 'lucide-react'
import { DuplicatePolicy, type CustomApplication } from '../../bindings/workspace-manager/internal/domain/models'
import { listApplications, type DesktopEntry } from '../services/applications'
import type { View, WorkspaceApplication } from '../services/workspaces'
import { ApplicationIcon, useApplicationIcons } from './ApplicationIcon'
import { Modal } from './Modal'
import { CustomApplicationEditor } from './CustomApplicationEditor'

export function WorkspaceApplications({ workspace, pending, blocked, error, onClose, onSave }: {
  workspace: View; pending: boolean; blocked: boolean; error: ReactNode; onClose: () => void; onSave: (choices: WorkspaceApplication[]) => void
}) {
  const [choices, setChoices] = useState<WorkspaceApplication[]>(() => (workspace.applications ?? []).map(choice => ({ ...choice, ...(choice.custom ? { custom: { ...choice.custom, arguments: [...(choice.custom.arguments ?? [])] } } : {}) })))
  const [customEditor, setCustomEditor] = useState<number | 'new' | null>(null)
  const [catalogue, setCatalogue] = useState<DesktopEntry[] | null>(null)
  const [catalogueError, setCatalogueError] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(24)
  const loadIcon = useApplicationIcons(attempt)
  const disabled = pending || blocked

  useEffect(() => {
    let current = true
    listApplications().then(result => {
      if (!current) return
      if (result.code) { setCatalogueError(result.message || 'Could not read installed applications.'); return }
      setCatalogue(result.entries ?? [])
    }, () => { if (current) setCatalogueError('Could not connect to the application catalogue. Your saved selections are kept.') })
    return () => { current = false }
  }, [attempt])

  function refresh() {
    setCatalogue(null)
    setCatalogueError('')
    setAttempt(value => value + 1)
  }
  function move(index: number, direction: number) {
    setChoices(previous => {
      const next = [...previous]
      const other = index + direction
      if (other < 0 || other >= next.length) return previous
      const moved = next[index]
      next[index] = next[other]
      next[other] = moved
      return next
    })
  }

  const selectedIDs = new Set(choices.map(choice => choice.desktopId))
  const search = query.trim().toLocaleLowerCase()
  const available = (catalogue ?? []).filter(entry => !selectedIDs.has(entry.id) && [entry.name, entry.genericName, entry.description, entry.id].some(value => value.toLocaleLowerCase().includes(search)))
  const nameFor = (id: string) => catalogue?.find(entry => entry.id === id)?.name || id
  const keepCustom = (custom: CustomApplication) => {
    setChoices(previous => customEditor === 'new' ? [...previous, { desktopId: '', custom, duplicatePolicy: DuplicatePolicy.SkipIfRunning }] : previous.map((choice, index) => index === customEditor ? { ...choice, custom } : choice))
    setCustomEditor(null)
  }

  return <Modal wide title={`Applications · ${workspace.name}`} description="Choose your applications, arrange their launch order and save duplicate-launch preferences. Saving does not open applications." busy={pending} onClose={onClose}>
    <div className="workspace-application-editor">
      <section aria-label="Workspace application order">
        <h3>Launch order <span className="muted">{choices.length} / 64</span></h3>
        {choices.length ? <ol className="selected-applications">{choices.map((choice, index) => {
          const name = choice.custom?.name || nameFor(choice.desktopId)
          const present = catalogue?.some(entry => entry.id === choice.desktopId)
          return <li key={choice.custom ? choice.custom.id || `draft:${index}` : choice.desktopId} aria-label={choice.custom ? `Custom application ${name}` : choice.desktopId}>
            <div className="selected-application-heading"><span className="app-order-index">{index + 1}</span>{choice.custom ? <span className="application-icon"><AppWindow size={24} aria-hidden="true" /></span> : <ApplicationIcon key={`${attempt}:${choice.desktopId}`} id={choice.desktopId} load={loadIcon} />}<div><strong>{name}</strong><p>{choice.custom ? 'Custom executable · checked at launch review' : catalogue === null ? 'Availability not checked' : present ? 'Installed' : 'Unavailable · kept in this workspace'}</p></div></div>
            <div className="selected-application-controls">
              <label>When already running<select aria-label={`Duplicate preference for ${name}`} value={choice.duplicatePolicy} disabled={disabled} onChange={event => setChoices(previous => previous.map((value, position) => position === index ? { ...value, duplicatePolicy: event.target.value as DuplicatePolicy } : value))}><option value={DuplicatePolicy.SkipIfRunning}>Skip a confirmed running instance</option><option value={DuplicatePolicy.AlwaysLaunch}>Always request a launch</option></select></label>
              <div className="flex gap-2">{choice.custom && <button type="button" className="secondary-button" disabled={disabled || customEditor !== null} onClick={() => setCustomEditor(index)} aria-label={`Edit custom application ${name}`}>Edit</button>}<button type="button" className="icon-button" aria-label={`Move ${name} up`} disabled={disabled || customEditor !== null || index === 0} onClick={() => move(index, -1)}><ArrowUp size={15} aria-hidden="true" /></button><button type="button" className="icon-button" aria-label={`Move ${name} down`} disabled={disabled || customEditor !== null || index === choices.length - 1} onClick={() => move(index, 1)}><ArrowDown size={15} aria-hidden="true" /></button><button type="button" className="icon-button" aria-label={`Remove ${name}`} disabled={disabled || customEditor !== null} onClick={() => setChoices(previous => previous.filter((_, position) => position !== index))}><Trash2 size={15} aria-hidden="true" /></button></div>
            </div>
          </li>
        })}</ol> : <p className="picker-empty">No applications selected. Add a tool from the installed list.</p>}
      </section>
      {customEditor !== null ? <CustomApplicationEditor key={customEditor} initial={typeof customEditor === 'number' ? choices[customEditor].custom ?? undefined : undefined} disabled={disabled} onCancel={() => setCustomEditor(null)} onSave={keepCustom} /> :
      <section aria-label="Add installed applications">
        <button type="button" className="secondary-button" disabled={disabled || choices.length >= 64} onClick={() => setCustomEditor('new')}>Add custom executable</button>
        <div className="picker-heading"><h3>Installed applications</h3><button type="button" className="icon-button" aria-label="Refresh installed applications" disabled={pending || (catalogue === null && !catalogueError)} onClick={refresh}><RefreshCw size={15} aria-hidden="true" /></button></div>
        <label className="search-field"><Search size={15} aria-hidden="true" /><input data-dialog-initial-focus type="search" aria-label="Find applications to add" placeholder="Search your tools…" disabled={pending} value={query} onChange={event => { setQuery(event.target.value); setLimit(24) }} /></label>
        {catalogueError ? <div className="error-panel" role="alert"><p>{catalogueError}</p><button type="button" className="secondary-button" disabled={pending} onClick={refresh}>Retry application discovery</button></div> : catalogue === null ? <p className="picker-empty" role="status">Reading installed applications…</p> : <>
          <ul className="picker-available">{available.slice(0, limit).map(entry => <li key={entry.id}><ApplicationIcon key={`${attempt}:${entry.id}`} id={entry.id} load={loadIcon} /><div><strong>{entry.name}</strong><p>{entry.id}</p></div><button type="button" className="icon-button" aria-label={`Add ${entry.name} (${entry.id})`} disabled={disabled || choices.length >= 64} onClick={() => setChoices(previous => previous.some(choice => choice.desktopId === entry.id) || previous.length >= 64 ? previous : [...previous, { desktopId: entry.id, duplicatePolicy: DuplicatePolicy.SkipIfRunning }])}><Plus size={16} aria-hidden="true" /></button></li>)}</ul>
          {available.length === 0 && <p className="picker-empty">{search ? 'No matching applications to add.' : 'No more installed applications to add.'}</p>}
          {available.length > limit && <button type="button" className="secondary-button" onClick={() => setLimit(value => value + 24)}>Show more applications</button>}
        </>}
      </section>}
    </div>
    <p className="draft-note">The order and preferences will apply to future workspace launches. Review saved custom tools individually from Applications. Running-instance checks use a supported D-Bus identity, never a process name.</p>
    {error}
    {blocked && <p className="draft-note">Your application draft is kept for reference. Reload workspaces, then close and reopen this editor to review the latest saved version.</p>}
    <div className="dialog-actions"><button type="button" className="secondary-button" disabled={pending} onClick={onClose}>Cancel</button><button type="button" className="primary-button" disabled={disabled || customEditor !== null} onClick={() => onSave(choices)}>{pending ? 'Saving…' : 'Save applications'}</button></div>
  </Modal>
}
