import { useEffect, useState } from 'react'
import { AppWindow, Check, FolderSearch, Layers3, Monitor, Moon, Sun } from 'lucide-react'
import { Workspaces } from './pages/Workspaces'
import { Diagnostics } from './pages/Diagnostics'
import { Applications } from './pages/Applications'
import { Projects } from './pages/Projects'
import './style.css'

function App() {
  const [page, setPage] = useState<'workspaces' | 'applications' | 'projects' | 'diagnostics'>('workspaces')
  const [dark, setDark] = useState(() => window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? true)
  useEffect(() => { document.documentElement.dataset.theme = dark ? 'dark' : 'light' }, [dark])

  return <div className="app-shell">
    <a className="skip-link" href="#main-content">Skip to content</a>
    <header className="app-header flex items-center justify-between gap-4">
      <div className="flex items-center gap-3"><span className="brand-mark"><Layers3 size={22} aria-hidden="true" /></span><div><p className="brand-title">Workspace Manager</p><p className="muted text-xs">Your development, together.</p></div></div>
      <button className="icon-button" aria-label={`Use ${dark ? 'light' : 'dark'} theme`} onClick={() => setDark(!dark)}>{dark ? <Sun size={18} aria-hidden="true" /> : <Moon size={18} aria-hidden="true" />}</button>
    </header>
    <nav className="page-nav" aria-label="Main navigation">
      <button data-page="workspaces" aria-current={page === 'workspaces' ? 'page' : undefined} onClick={() => setPage('workspaces')}><Layers3 size={16} aria-hidden="true" />Workspaces</button>
      <button data-page="applications" aria-current={page === 'applications' ? 'page' : undefined} onClick={() => setPage('applications')}><AppWindow size={16} aria-hidden="true" />Applications</button>
      <button data-page="projects" aria-current={page === 'projects' ? 'page' : undefined} onClick={() => setPage('projects')}><FolderSearch size={16} aria-hidden="true" />Projects</button>
      <button data-page="diagnostics" aria-current={page === 'diagnostics' ? 'page' : undefined} onClick={() => setPage('diagnostics')}><Monitor size={16} aria-hidden="true" />Diagnostics</button>
      <span className="phase-label">PHASE 05 / PROJECT CONFIGURATION</span>
    </nav>
    <main id="main-content" tabIndex={-1}>{page === 'workspaces' ? <Workspaces /> : page === 'applications' ? <Applications /> : page === 'projects' ? <Projects /> : <Diagnostics />}</main>
    <footer className="app-footer flex flex-wrap items-center justify-between gap-3"><span className="flex items-center gap-2"><Check size={14} aria-hidden="true" />Local by design. No account required.</span><span>Built for Linux · GNOME</span></footer>
  </div>
}

export default App
