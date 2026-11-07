import { useEffect, useRef, useState } from 'react'
import { Activity, AppWindow, ArrowRight, Clock3, FolderCode, Layers3, Monitor, Moon, Power, Save, Search, Settings2, ShieldCheck, Sun } from 'lucide-react'
import { Workspaces } from './pages/Workspaces'
import { Diagnostics } from './pages/Diagnostics'
import { Applications } from './pages/Applications'
import { Projects } from './pages/Projects'
import { Lifecycle, type LifecyclePageName } from './pages/Lifecycle'
import { WorkspaceOverview } from './components/WorkspaceOverview'
import { Modal } from './components/Modal'
import appIcon from './assets/app-icon.svg'
import './style.css'

type Page = LifecyclePageName | 'workspaces' | 'applications' | 'projects' | 'diagnostics'
const pages = [
  { id: 'workspaces', label: 'Workspaces', icon: Layers3 },
  { id: 'projects', label: 'Projects', icon: FolderCode },
  { id: 'sessions', label: 'Saved sessions', icon: Clock3 },
  { id: 'activity', label: 'Activity', icon: Activity },
  { id: 'settings', label: 'Settings', icon: Settings2 },
] as const
const tools = [{ id: 'applications', label: 'App library', icon: AppWindow }, { id: 'diagnostics', label: 'Diagnostics', icon: Monitor }] as const
const preferredDark = () => {
  const value = localStorage.getItem('workspace-manager-appearance')
  return value === 'dark' || (value !== 'light' && (window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? true))
}

function App() {
  const [page, setPage] = useState<Page>('workspaces')
  const [intent, setIntent] = useState<{ page: Page; request: number; action: 'create' | 'shutdown' } | null>(null)
  const sequence = useRef(0)
  const [dark, setDark] = useState(preferredDark)
  const [searching, setSearching] = useState(false)
  const [query, setQuery] = useState('')
  useEffect(() => { document.documentElement.dataset.theme = dark ? 'dark' : 'light' }, [dark])
  useEffect(() => {
    const update = () => setDark(preferredDark())
    const media = window.matchMedia?.('(prefers-color-scheme: dark)')
    window.addEventListener('workspace-manager-appearance', update)
    media?.addEventListener('change', update)
    return () => { window.removeEventListener('workspace-manager-appearance', update); media?.removeEventListener('change', update) }
  }, [])
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        if (document.querySelector('dialog[open]')) return
        event.preventDefault(); setQuery(''); setSearching(true)
      }
    }
    window.addEventListener('keydown', shortcut)
    return () => window.removeEventListener('keydown', shortcut)
  }, [])
  function navigate(next: Page, create = false) {
    setSearching(false); setPage(next)
    setIntent(create ? { page: next, request: ++sequence.current, action: 'create' } : null)
  }
  function reviewShutdown() {
    setSearching(false); setPage('sessions'); setIntent({ page: 'sessions', request: ++sequence.current, action: 'shutdown' })
  }
  useEffect(() => {
    document.documentElement.scrollTop = 0
    document.body.scrollTop = 0
    if (!document.querySelector('dialog[open]')) document.getElementById('main-content')?.focus({ preventScroll: true })
  }, [page])
  const request = intent?.page === page ? intent.request : 0
  const actions = [
    { label: 'Create project', description: 'Start from a project template', icon: FolderCode, run: () => navigate('projects', true) },
    { label: 'Create workspace', description: 'Group your projects and applications', icon: Layers3, run: () => navigate('workspaces', true) },
    { label: 'Save & Shutdown', description: 'Review saving before normal desktop shutdown', icon: Power, run: reviewShutdown },
    { label: 'Save session', description: 'Capture your current supported setup', icon: Save, run: () => navigate('sessions', true) },
    ...[...pages, ...tools].map(item => ({ label: item.label, description: `Open ${item.label.toLowerCase()}`, icon: item.icon, run: () => navigate(item.id) })),
  ].filter(item => `${item.label} ${item.description}`.toLowerCase().includes(query.trim().toLowerCase()))

  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to content</a>
    <aside className="app-sidebar">
      <div className="app-brand"><img src={appIcon} width="40" height="40" alt="" /><div><p className="brand-title">Workspace<span>Manager</span></p></div></div>
      <p className="nav-caption">YOUR WORK</p>
      <nav className="page-nav" aria-label="Main navigation">{pages.slice(0, 3).map(({ id, label, icon: Icon }) => <button key={id} data-page={id} aria-label={label} title={label} aria-current={page === id ? 'page' : undefined} onClick={() => navigate(id)}><Icon size={19} aria-hidden="true" /><span>{label}</span></button>)}</nav>
      <nav className="page-nav utility-nav" aria-label="Tools and settings">{pages.slice(3).map(({ id, label, icon: Icon }) => <button key={id} data-page={id} aria-label={label} title={label} aria-current={page === id || (id === 'settings' && tools.some(tool => tool.id === page)) ? 'page' : undefined} onClick={() => navigate(id)}><Icon size={18} aria-hidden="true" /><span>{label}</span></button>)}</nav>
      <div className="sidebar-bottom"><ShieldCheck size={16} aria-hidden="true" /><span>Local by design<small>Your work stays on this device.</small></span></div>
    </aside>
    <div className="app-main">
      <header className="app-header">
        <button className="quick-search secondary-button" aria-label="Quick actions" aria-keyshortcuts="Control+k Meta+k" onClick={() => { setQuery(''); setSearching(true) }}><Search size={16} aria-hidden="true" /><span>Quick actions</span><kbd>Ctrl K</kbd></button>
        <div className="global-actions">
          {page !== 'sessions' && <button className="secondary-button" aria-label="Save session" title="Save session" onClick={() => navigate('sessions', true)}><Save size={16} aria-hidden="true" /><span>Save session</span></button>}
          {page !== 'sessions' && <button className="secondary-button" aria-label="Save & Shutdown" title="Save & Shutdown" onClick={reviewShutdown}><Power size={16} aria-hidden="true" /><span>Save & Shutdown</span></button>}
          <button className="icon-button" aria-label={`Use ${dark ? 'light' : 'dark'} theme`} title={`Use ${dark ? 'light' : 'dark'} theme`} onClick={() => { localStorage.setItem('workspace-manager-appearance', dark ? 'light' : 'dark'); setDark(!dark) }}>{dark ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}</button>
        </div>
      </header>
      <main id="main-content" tabIndex={-1}>
        {(page === 'applications' || page === 'diagnostics') && <button className="text-button back-to-settings" onClick={() => navigate('settings')}>← Back to settings</button>}
        {page === 'workspaces' ? <Workspaces createRequest={request} overview={<WorkspaceOverview onNavigate={navigate} />} /> : page === 'applications' ? <Applications /> : page === 'projects' ? <Projects createRequest={request} /> : page === 'diagnostics' ? <Diagnostics /> : <Lifecycle key={page} page={page} captureRequest={intent?.action === 'create' ? request : 0} shutdownRequest={intent?.action === 'shutdown' ? request : 0} />}
        {page === 'settings' && <section className="settings-tools" aria-label="Supporting tools"><h2>Tools & help</h2><p className="muted">Browse installed apps or check your desktop connection.</p><div className="settings-tool-links">{tools.map(({ id, label, icon: Icon }) => <button key={id} className="secondary-button" data-page={id} onClick={() => navigate(id)}><Icon size={17} aria-hidden="true" />{label}<ArrowRight size={15} aria-hidden="true" /></button>)}</div></section>}
      </main>
      <footer className="app-footer"><span>Workspace Manager</span><span>Linux · GNOME</span></footer>
    </div>
    {searching && <Modal title="Quick actions" description="Type to filter. Press Enter for the first result or Tab to choose another." busy={false} onClose={() => setSearching(false)}><label className="search-field command-search"><Search size={18} aria-hidden="true" /><input data-dialog-initial-focus aria-label="Search actions" placeholder="What would you like to do?" value={query} onChange={e => setQuery(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && actions.length) { e.preventDefault(); actions[0].run() } }} /></label><div className="command-results">{actions.length ? actions.map(({ label, description, icon: Icon, run }) => <button key={label} className="command-action" onClick={run}><Icon size={19} aria-hidden="true" /><span><strong>{label}</strong><small>{description}</small></span><ArrowRight size={16} aria-hidden="true" /></button>) : <p className="picker-empty">No matching actions. Try “project” or “session”.</p>}</div></Modal>}
  </div>
}
export default App
