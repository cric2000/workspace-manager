import { useEffect, useState, type ReactNode } from 'react'
import { Modal } from './Modal'
import { listProjects, type ProjectView } from '../services/projects'
import type { ProjectSelection, View } from '../services/workspaces'

export function WorkspaceProjects({ workspace, pending, blocked, error, onClose, onSave }: {
  workspace: View; pending: boolean; blocked: boolean; error: ReactNode; onClose: () => void; onSave: (choices: ProjectSelection[]) => void
}) {
  const [selected, setSelected] = useState<string[]>([...(workspace.projectIds ?? [])])
  const [projects, setProjects] = useState<ProjectView[]>([])
  const [loading, setLoading] = useState(true)
  const [loaded, setLoaded] = useState(false)
  const [fault, setFault] = useState('')
  const [attempt, setAttempt] = useState(0)
  const [query, setQuery] = useState('')
  const [limit, setLimit] = useState(24)
  useEffect(() => {
    let current = true
    setLoading(true); setFault(''); setLoaded(false)
    listProjects().then(result => {
      if (!current) return
      if (result.code || !result.state) setFault(result.message || 'Could not load registered projects.')
      else { setProjects(result.state.projects ?? []); setLoaded(true) }
      setLoading(false)
    }, () => { if (current) { setFault('Could not connect to project storage. Reload the project list to try again.'); setLoading(false) } })
    return () => { current = false }
  }, [attempt])
  const disabled = pending || blocked || loading
  const available = projects.filter(view => !selected.includes(view.project.id) && `${view.project.name} ${view.project.path}`.toLocaleLowerCase().includes(query.toLocaleLowerCase()))
  function move(index: number, offset: number) {
    const next = [...selected]; [next[index], next[index + offset]] = [next[index + offset], next[index]]; setSelected(next)
  }
  return <Modal title={`Projects for ${workspace.name}`} description="Choose registered projects in workspace order. Registrations are shared across workspaces. Saving starts nothing and leaves project files in place." busy={pending} onClose={onClose} wide>
    <section aria-label="Selected projects"><h3>Workspace projects ({selected.length} / 64)</h3>
      {!selected.length && <p className="picker-empty">No projects selected.</p>}
      <ol className="project-selection-list">{selected.map((id, index) => {
        const view = projects.find(item => item.project.id === id)
        const name = view?.project.name || `Unavailable project ${id}`
        return <li className="project-command-editor" key={id}><h4>{index + 1}. {name}</h4><p className="project-location">{view?.project.path || (loaded ? 'Existing reference kept. You can remove it explicitly.' : id)}</p><div className="project-command-actions">
          <button className="secondary-button" disabled={disabled || index === 0} onClick={() => move(index, -1)} aria-label={`Move ${name} up`}>Move up</button>
          <button className="secondary-button" disabled={disabled || index === selected.length - 1} onClick={() => move(index, 1)} aria-label={`Move ${name} down`}>Move down</button>
          <button className="secondary-button" disabled={disabled} onClick={() => setSelected(selected.filter(value => value !== id))} aria-label={`Remove ${name} from workspace`}>Remove</button>
        </div></li>
      })}</ol>
    </section>
    <section aria-label="Available projects"><div className="project-library-heading"><h3>Registered projects</h3><button className="secondary-button" disabled={disabled} onClick={() => setAttempt(attempt + 1)}>Reload project list</button></div>
      <label className="editor-fields">Search registered projects<input type="search" data-dialog-initial-focus disabled={pending || blocked} value={query} onChange={event => { setQuery(event.target.value); setLimit(24) }} /></label>
      {loading && <p role="status">Loading registered projects…</p>}
      {fault && <p className="error-panel" role="alert">{fault}</p>}
      {loaded && !available.length && <p className="picker-empty">No more matching projects. Import a project from Projects to add it here.</p>}
      {loaded && <div className="project-stack-grid">{available.slice(0, limit).map(view => <button className="application-choice" key={view.project.id} disabled={disabled || selected.length >= 64} onClick={() => setSelected([...selected, view.project.id])} aria-label={`Add ${view.project.name} to workspace`}><span className="project-choice-label"><strong>{view.project.name}</strong><span className="project-location">{view.project.path}</span></span></button>)}</div>}
      {loaded && available.length > limit && <button className="secondary-button" disabled={disabled} onClick={() => setLimit(limit + 24)}>Show more projects</button>}
    </section>
    {error}{blocked && <p className="draft-note">Your selection is kept for reference. Reload workspaces, then close and reopen this picker before saving.</p>}
    <div className="dialog-actions"><button className="secondary-button" disabled={pending} onClick={onClose}>Cancel</button><button className="primary-button" disabled={disabled || !loaded || selected.length > 64} onClick={() => onSave(selected.map(id => ({ id, revision: projects.find(view => view.project.id === id)?.revision ?? '' })))}>Save workspace projects</button></div>
  </Modal>
}
