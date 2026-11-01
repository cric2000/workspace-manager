import { useRef, useState } from 'react'
import { Modal } from './Modal'
import { removeProject, type ProjectView, type RegistryResult } from '../services/projects'

export function ProjectRemoval({ view, onClose, onRemoved }: { view: ProjectView; onClose: () => void; onRemoved: (result: RegistryResult) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [blocked, setBlocked] = useState(false)
  const working = useRef(false)
  async function remove() {
    if (working.current || blocked) return
    working.current = true; setBusy(true)
    try {
      const result = await removeProject({ id: view.project.id, revision: view.revision })
      if (result.committed) { onRemoved(result); return }
      setError(result.message || 'The registration could not be removed. Close and reload registered projects before trying again.')
      setBlocked(true)
    } catch { setError('The removal response was lost. Close and reload registered projects to check the saved state before trying again.'); setBlocked(true) }
    finally { working.current = false; setBusy(false) }
  }
  return <Modal title="Remove project registration" description="Removes the saved registration only. Project directories and files are kept. Remove references from workspace profiles first." busy={busy} onClose={onClose}>
    <h3>{view.project.name}</h3><p className="project-location">{view.project.path}</p>
    {error && <p className="error-panel" role="alert">{error}</p>}
    <div className="dialog-actions"><button className="secondary-button" data-dialog-initial-focus disabled={busy} onClick={onClose}>Cancel</button><button className="danger-button" disabled={busy || blocked} onClick={() => void remove()}>{busy ? 'Removing…' : 'Remove registration'}</button></div>
  </Modal>
}
