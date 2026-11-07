import { useEffect, useState } from 'react'
import { Activity, ArrowRight, Clock3 } from 'lucide-react'
import { execute, type Response } from '../services/lifecycle'
import { LaunchControl } from './LaunchControl'

export function WorkspaceOverview({ onNavigate }: { onNavigate: (page: 'sessions' | 'activity' | 'settings') => void }) {
  const [state, setState] = useState<Response | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let live = true, loading = false
    async function refresh() {
      if (loading || document.querySelector('dialog[open]')) return
      loading = true
      try {
        const result = await execute({ action: 'state' })
        if (!live) return
        if (result.code) setError(result.message)
        else { setState(result); setError('') }
      } catch { if (live) setError('Activity is unavailable. Open Activity to reconnect.') }
      finally { loading = false }
    }
    void refresh()
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => { live = false; window.clearInterval(timer) }
  }, [])
  const settings = state?.settings
  const startupWorkspace = state?.workspaces?.find(item => item.id === settings?.defaultWorkspaceId)
  const currentWorkspace = state?.workspaces?.find(item => item.id === state.runtime?.activeWorkspaceId)
  const recent = state?.sessions?.[0]?.value
  const running = state?.runtime?.runs?.filter(item => item.state !== 'stopped').length ?? 0
  const failed = state?.runtime?.tasks?.filter(item => item.state === 'failed').length ?? 0
  const startup = !settings?.autostart ? 'Off' : settings.startupMode === 'restore' ? 'Saved session' : settings.startupMode === 'hybrid' ? 'Workspace + saved session' : startupWorkspace?.name || 'Workspace unavailable'
  return <section className="workspace-overview" aria-label="Workspace overview">
    <div className="home-status">
      <button className="home-status-item" onClick={() => onNavigate('activity')}><Activity size={19} aria-hidden="true" /><span><strong>{state ? failed ? `${failed} launch ${failed === 1 ? 'issue' : 'issues'}` : state.runtime?.busy ? 'Launching your setup…' : currentWorkspace ? `${currentWorkspace.name} workspace` : 'Ready to launch' : error ? 'Check activity' : 'Connecting…'}</strong><small>{error || `${running} running ${running === 1 ? 'project service' : 'project services'} · View activity`}</small></span><ArrowRight size={15} aria-hidden="true" /></button>
      <button className="home-status-item" onClick={() => onNavigate('settings')}><Clock3 size={19} aria-hidden="true" /><span><strong>Login startup: {state ? startup : '…'}</strong><small>{settings?.autostart ? 'Manage what opens after login' : 'Choose a setup to open after login'}</small></span><ArrowRight size={15} aria-hidden="true" /></button>
    </div>
    {recent && <div className="resume-session"><div><p className="eyebrow">CONTINUE A SAVED SESSION</p><h2>{recent.name}</h2><p className="muted text-xs">{new Date(recent.capturedAt).toLocaleString()} · {(recent.applications ?? []).length} apps · {(recent.projects ?? []).length} projects</p></div><LaunchControl kind="session" id={recent.id} name={recent.name} /><button className="text-button" onClick={() => onNavigate('sessions')}>All saved sessions</button></div>}
  </section>
}
