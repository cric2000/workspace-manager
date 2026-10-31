import { useEffect, useState } from 'react'
import { AppWindow, Check, ChevronLeft, ChevronRight, RefreshCw, Search } from 'lucide-react'
import { listApplications, type DesktopEntry } from '../services/applications'
import { ApplicationIcon, useApplicationIcons } from '../components/ApplicationIcon'
import { ApplicationLaunch } from '../components/ApplicationLaunch'
import { SavedCustomApplications } from '../components/SavedCustomApplications'

type Catalogue = { status: 'loading' } | { status: 'ready'; entries: DesktopEntry[] } | { status: 'error'; message: string }
const PAGE_SIZE = 24

export function Applications() {
  const [catalogue, setCatalogue] = useState<Catalogue>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(0)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const loadIcon = useApplicationIcons(attempt)

  useEffect(() => {
    let current = true
    setCatalogue({ status: 'loading' })
    listApplications().then(result => {
      if (!current) return
      if (result.code) { setCatalogue({ status: 'error', message: result.message || 'Could not read installed applications.' }); return }
      const entries = result.entries ?? []
      setCatalogue({ status: 'ready', entries })
      setSelectedId(id => entries.some(entry => entry.id === id) ? id : null)
      setPage(0)
    }, () => {
      if (current) setCatalogue({ status: 'error', message: 'Could not connect to the desktop service. Open the native application and try again.' })
    })
    return () => { current = false }
  }, [attempt])

  const entries = catalogue.status === 'ready' ? catalogue.entries : []
  const search = query.trim().toLocaleLowerCase()
  const filtered = entries.filter(entry => [entry.name, entry.genericName, entry.description, entry.id].some(value => value.toLocaleLowerCase().includes(search)))
  const pages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE))
  const currentPage = Math.min(page, pages - 1)
  const shown = filtered.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE)
  const selected = entries.find(entry => entry.id === selectedId)
  const loading = catalogue.status === 'loading'
  const refresh = () => { setCatalogue({ status: 'loading' }); setAttempt(value => value + 1) }

  return <>
    <section className="workspace-heading" aria-labelledby="applications-title">
      <div><p className="eyebrow">TOOLS ON THIS COMPUTER</p><h1 id="applications-title">Applications</h1><p className="muted">Find your tools and inspect how they open.</p></div>
      <button className="secondary-button" disabled={loading} onClick={refresh}><RefreshCw size={15} aria-hidden="true" />Refresh applications</button>
    </section>
    <p className="catalogue-status" role="status" aria-live="polite">{loading ? 'Reading installed applications…' : catalogue.status === 'ready' ? `${entries.length} installed ${entries.length === 1 ? 'application' : 'applications'}` : ''}</p>
    {catalogue.status === 'error' && <div className="error-panel" role="alert"><p>{catalogue.message}</p><button className="secondary-button" onClick={refresh}>Retry discovery</button></div>}
    {catalogue.status === 'ready' && <>
      <div className="application-search"><label className="search-field"><Search size={17} aria-hidden="true" /><input type="search" aria-label="Search installed applications" placeholder="Search by name or application ID…" value={query} onChange={event => { setQuery(event.target.value); setPage(0) }} /></label><p className="muted text-xs">{filtered.length} {filtered.length === 1 ? 'match' : 'matches'}</p></div>
      {entries.length ? <div className="application-browser">
        <section aria-label="Installed applications">
          {shown.length ? <ul className="application-list">{shown.map(entry => <li key={entry.id}>
            <button className="application-choice" aria-pressed={entry.id === selectedId} aria-label={`Inspect ${entry.name} (${entry.id})`} onClick={() => setSelectedId(entry.id)}>
              <ApplicationIcon key={`${attempt}:${entry.id}`} id={entry.id} load={loadIcon} />
              <span className="application-label"><strong>{entry.name}</strong><span>{entry.genericName || entry.description || entry.id}</span></span><ChevronRight size={15} className="muted" aria-hidden="true" />
            </button>
          </li>)}</ul> : <div className="panel catalogue-empty"><Search size={24} aria-hidden="true" /><h2>No matching applications</h2><p>Try a different name or application ID.</p></div>}
          {pages > 1 && <nav className="application-pagination" aria-label="Application pages"><button className="icon-button" aria-label="Previous application page" disabled={currentPage === 0} onClick={() => setPage(value => value - 1)}><ChevronLeft size={16} aria-hidden="true" /></button><span>Page {currentPage + 1} of {pages}</span><button className="icon-button" aria-label="Next application page" disabled={currentPage >= pages - 1} onClick={() => setPage(value => value + 1)}><ChevronRight size={16} aria-hidden="true" /></button></nav>}
        </section>
        <section className="panel application-details" aria-label="Application details">
          {selected ? <>
            <div className="application-detail-heading"><ApplicationIcon key={`${attempt}:${selected.id}`} id={selected.id} load={loadIcon} /><div><h2>{selected.name}</h2><p className="installed-label"><Check size={13} aria-hidden="true" />Installed</p></div></div>
            {selected.description && <p className="application-detail-description">{selected.description}</p>}
            <dl>
              <div><dt>Application ID</dt><dd>{selected.id}</dd></div>
              <div><dt>Opens with</dt><dd>{selected.dbusActivatable ? 'Desktop activation (D-Bus)' : selected.executable || 'Desktop launcher'}</dd></div>
              <div><dt>Terminal window</dt><dd>{selected.terminal ? 'Required' : 'Not required'}</dd></div>
              {selected.commandline && <div><dt>{selected.dbusActivatable ? 'Fallback launch entry' : 'Launch entry'}</dt><dd className="launch-entry">{selected.commandline}</dd></div>}
              <div><dt>Desktop file</dt><dd>{selected.desktopFile}</dd></div>
            </dl>
            <p className="muted text-xs detail-note">Installed means a desktop entry is registered. Running state and readiness are not verified.</p>
            <ApplicationLaunch key={`${attempt}:${selected.id}`} id={selected.id} />
          </> : <div className="detail-placeholder"><AppWindow size={29} aria-hidden="true" /><h2>Get to know your tools</h2><p>Select an application to inspect its launch entry and desktop details.</p></div>}
        </section>
      </div> : <div className="panel catalogue-empty"><AppWindow size={28} aria-hidden="true" /><h2>No applications found</h2><p>No visible application entries were found for this desktop. Install an application or check your desktop data directories, then refresh.</p></div>}
    </>}
    <SavedCustomApplications />
    <aside className="scope-note"><h2>Your tools, ready to organize.</h2><p>Review and launch an individual application here. D-Bus identities provide conservative running-instance checks. To save tools in a profile, open Workspaces and choose Apps. Ordered workspace execution remains planned.</p></aside>
  </>
}

