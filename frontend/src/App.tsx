import { useEffect, useRef, useState } from 'react'
import { Activity, AppWindow, ArrowRight, Clock3, FolderCode, House, Layers3, Monitor, Moon, Plus, Save, Search, Settings2, ShieldCheck, Sun } from 'lucide-react'
import { Workspaces } from './pages/Workspaces'
import { Diagnostics } from './pages/Diagnostics'
import { Applications } from './pages/Applications'
import { Projects } from './pages/Projects'
import { Lifecycle, type LifecyclePageName } from './pages/Lifecycle'
import { Modal } from './components/Modal'
import appIcon from './assets/app-icon.svg'
import './style.css'

type Page = LifecyclePageName | 'workspaces' | 'applications' | 'projects' | 'diagnostics'
const pages = [
  { id: 'dashboard', label: 'Dashboard', icon: House },
  { id: 'workspaces', label: 'Workspaces', icon: Layers3 },
  { id: 'projects', label: 'Projects', icon: FolderCode },
  { id: 'applications', label: 'Applications', icon: AppWindow },
  { id: 'sessions', label: 'Sessions', icon: Clock3 },
  { id: 'activity', label: 'Activity', icon: Activity },
  { id: 'settings', label: 'Settings', icon: Settings2 },
  { id: 'diagnostics', label: 'Diagnostics', icon: Monitor },
] as const
const preferredDark = () => {
  const value = localStorage.getItem('workspace-manager-appearance')
  return value === 'dark' || (value !== 'light' && (window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? true))
}

function App() {
  const [page, setPage] = useState<Page>('dashboard')
  const [intent, setIntent] = useState<{ page: Page; request: number } | null>(null)
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
    setIntent(create ? { page: next, request: ++sequence.current } : null)
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
    { label: 'Save session', description: 'Capture your current supported setup', icon: Save, run: () => navigate('sessions', true) },
    ...pages.map(item => ({ label: item.label, description: `Open ${item.label.toLowerCase()}`, icon: item.icon, run: () => navigate(item.id) })),
  ].filter(item => `${item.label} ${item.description}`.toLowerCase().includes(query.trim().toLowerCase()))

  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to content</a>
    <aside className="app-sidebar">
      <div className="app-brand"><img src={appIcon} width="40" height="40" alt="" /><div><p className="brand-title">Workspace<span>Manager</span></p></div></div>
      <p className="nav-caption">WORKSPACE</p>
      <nav className="page-nav" aria-label="Main navigation">{pages.map(({ id, label, icon: Icon }, index) => <button key={id} data-page={id} className={index === 6 ? 'nav-divider' : undefined} aria-label={label} title={label} aria-current={page === id ? 'page' : undefined} onClick={() => navigate(id)}><Icon size={19} aria-hidden="true" /><span>{label}</span></button>)}</nav>
      <div className="sidebar-bottom"><ShieldCheck size={16} aria-hidden="true" /><span>Local by design<small>Your work stays on this device.</small></span></div>
    </aside>
    <div className="app-main">
      <header className="app-header">
        <button className="quick-search secondary-button" aria-label="Quick actions" aria-keyshortcuts="Control+k Meta+k" onClick={() => { setQuery(''); setSearching(true) }}><Search size={16} aria-hidden="true" /><span>Quick actions</span><kbd>Ctrl K</kbd></button>
        <div className="global-actions">
          <button className="secondary-button" aria-label="Save session" title="Save session" onClick={() => navigate('sessions', true)}><Save size={16} aria-hidden="true" /><span>Save session</span></button>
          <button className="secondary-button" aria-label="Create workspace" title="Create workspace" onClick={() => navigate('workspaces', true)}><Layers3 size={16} aria-hidden="true" /><span>New workspace</span></button>
          <button className="primary-button" aria-label="Create project" title="Create project" onClick={() => navigate('projects', true)}><Plus size={17} aria-hidden="true" /><span>New project</span></button>
          <button className="icon-button" aria-label={`Use ${dark ? 'light' : 'dark'} theme`} title={`Use ${dark ? 'light' : 'dark'} theme`} onClick={() => { localStorage.setItem('workspace-manager-appearance', dark ? 'light' : 'dark'); setDark(!dark) }}>{dark ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}</button>
        </div>
      </header>
      <main id="main-content" tabIndex={-1}>{page === 'workspaces' ? <Workspaces createRequest={request} /> : page === 'applications' ? <Applications /> : page === 'projects' ? <Projects createRequest={request} /> : page === 'diagnostics' ? <Diagnostics /> : <Lifecycle key={page} page={page} captureRequest={request} onNavigate={navigate} />}</main>
      <footer className="app-footer"><span>Workspace Manager</span><span>Linux · GNOME</span></footer>
    </div>
    {searching && <Modal title="Quick actions" description="Type to filter. Press Enter for the first result or Tab to choose another." busy={false} onClose={() => setSearching(false)}><label className="search-field command-search"><Search size={18} aria-hidden="true" /><input data-dialog-initial-focus aria-label="Search actions" placeholder="What would you like to do?" value={query} onChange={e => setQuery(e.target.value)} onKeyDown={e => { if (e.key === 'Enter' && actions.length) { e.preventDefault(); actions[0].run() } }} /></label><div className="command-results">{actions.length ? actions.map(({ label, description, icon: Icon, run }) => <button key={label} className="command-action" onClick={run}><Icon size={19} aria-hidden="true" /><span><strong>{label}</strong><small>{description}</small></span><ArrowRight size={16} aria-hidden="true" /></button>) : <p className="picker-empty">No matching actions. Try “project” or “session”.</p>}</div></Modal>}
  </div>
}
export default App
