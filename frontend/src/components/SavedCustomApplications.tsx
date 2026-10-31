import { useEffect, useState } from 'react'
import { listWorkspaces, type View } from '../services/workspaces'
import { ApplicationLaunch } from './ApplicationLaunch'

export function SavedCustomApplications() {
  const [workspaces, setWorkspaces] = useState<View[]>([])
  const [attempt, setAttempt] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    let current = true
    setLoading(true); setError('')
    listWorkspaces().then(result => {
      if (!current) return
      setLoading(false)
      if (result.code || !result.state) { setError(result.message || 'Could not read saved custom tools.'); return }
      setWorkspaces(result.state.workspaces ?? [])
    }, () => { if (current) { setLoading(false); setError('Could not connect to saved workspace tools. Try refreshing.') } })
    return () => { current = false }
  }, [attempt])
  const saved = workspaces.flatMap(workspace => (workspace.applications ?? []).filter(choice => choice.custom).map(choice => ({ workspace, choice })))
  return <section className="saved-custom-applications" aria-label="Saved custom applications"><div className="picker-heading"><h2>Custom applications</h2><button className="secondary-button" disabled={loading} onClick={() => setAttempt(value => value + 1)}>Refresh saved tools</button></div>
    <p className="muted text-xs">Configure custom executable paths and literal arguments in a workspace’s Apps editor. Review each saved tool here before launching.</p>
    {loading ? <p className="muted">Reading saved tools…</p> : error ? <p className="error-panel" role="alert">{error}</p> : saved.length ? <div className="custom-application-grid">{saved.map(({ workspace, choice }) => <article className="panel" key={`${workspace.id}:${workspace.revision}:${choice.custom!.id}`} aria-label={`Custom tool ${choice.custom!.name} in ${workspace.name}`}><h3>{choice.custom!.name}</h3><p className="muted text-xs">{workspace.name}</p><p className="custom-executable">{choice.custom!.executable}</p><ApplicationLaunch customTarget={{ workspaceId: workspace.id, revision: workspace.revision, customId: choice.custom!.id }} initialPolicy={choice.duplicatePolicy} /></article>)}</div> : <p className="picker-empty">No saved custom applications.</p>}
  </section>
}
