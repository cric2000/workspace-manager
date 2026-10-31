import { useEffect, useState } from 'react'
import { ArrowRight, CircleAlert, Monitor, RefreshCw, Terminal } from 'lucide-react'
import { getInfo, type Info } from '../services/desktop'

type Connection = { status: 'loading' } | { status: 'ready'; info: Info } | { status: 'error' }

const milestones = [
  ['01', 'Desktop foundation', 'Native shell, typed bridge and build checks.'],
  ['02', 'Models & local storage', 'Profiles, projects and versioned session snapshots.'],
  ['03', 'Workspace management', 'Create, organize and edit reusable environments.'],
] as const

export function Diagnostics() {
  const [connection, setConnection] = useState<Connection>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    let current = true
    setConnection({ status: 'loading' })
    getInfo().then(
      info => { if (current) setConnection({ status: 'ready', info }) },
      () => { if (current) setConnection({ status: 'error' }) },
    )
    return () => { current = false }
  }, [attempt])

  const ready = connection.status === 'ready'

  return (
    <>
        <section className="intro" aria-labelledby="welcome-title">
          <p className="eyebrow">A LOCAL-FIRST DEVELOPER WORKSPACE</p>
          <h1 id="welcome-title">A place to pick up<br /><span>where you left off.</span></h1>
          <p className="intro-copy">Bring your projects, tools and saved sessions together.<br className="hidden sm:block" /> Built for the way you develop on Linux.</p>
          <div className="connection-pill" role="status" aria-live="polite">
            <span className={`status-dot ${ready ? 'connected' : ''}`} />
            {ready ? 'Desktop bridge connected' : connection.status === 'error' ? 'Desktop bridge unavailable' : 'Connecting to desktop…'}
          </div>
        </section>

        <div className="content-grid">
          <section className="panel environment-panel" aria-labelledby="environment-title" aria-busy={connection.status === 'loading'}>
            <div className="flex items-start justify-between gap-4">
              <div><p className="eyebrow">ON THIS MACHINE</p><h2 id="environment-title">Desktop environment</h2></div>
              <Monitor size={21} className="muted" aria-hidden="true" />
            </div>
            {connection.status === 'ready' ? (
              <dl className="diagnostics">
                {([
                  ['Platform', `${connection.info.os} / ${connection.info.architecture}`],
                  ['Desktop session', connection.info.sessionType],
                  ['Go runtime', connection.info.goVersion],
                  ['Application', connection.info.version],
                ] as const).map(([label, value]) => (
                  <div key={label}><dt>{label}</dt><dd>{value}</dd></div>
                ))}
              </dl>
            ) : connection.status === 'error' ? (
              <div className="bridge-error" role="alert">
                <CircleAlert size={20} aria-hidden="true" />
                <div><p className="font-medium">Could not connect to the Go service.</p><p className="muted text-sm mt-2">Open the native application using the project’s desktop run command. A browser preview cannot verify the native bridge.</p></div>
              </div>
            ) : <p className="loading-copy">Waiting for runtime diagnostics…</p>}
            <button className="secondary-button" disabled={connection.status === 'loading'} onClick={() => setAttempt(value => value + 1)}>
              <RefreshCw size={15} aria-hidden="true" /> {connection.status === 'error' ? 'Retry connection' : 'Refresh diagnostics'}
            </button>
          </section>

          <section className="panel journey-panel" aria-labelledby="journey-title">
            <p className="eyebrow">BUILDING ONE MILESTONE AT A TIME</p>
            <h2 id="journey-title">The road to your workspace</h2>
            <ol className="milestones">
              {milestones.map(([number, title, description], index) => (
                <li key={number}>
                  <span className={`milestone-number ${index === 2 ? 'current' : ''}`}>{number}</span>
                  <div><h3>{title}{index === 2 && <span className="current-label">CURRENT</span>}</h3><p>{description}</p></div>
                </li>
              ))}
            </ol>
            <p className="next-note flex items-center gap-2"><ArrowRight size={15} aria-hidden="true" /> Next: application discovery and launch.</p>
          </section>
        </div>

        <section className="scope-note flex gap-3" aria-labelledby="scope-title">
          <Terminal size={20} className="shrink-0 mt-1" aria-hidden="true" />
          <div><h2 id="scope-title">Profiles now persist locally.</h2><p>This build saves workspace profiles and verifies the Go connection. Application launching, project execution, session restore and shutdown arrive in later milestones.</p></div>
        </section>
    </>
  )
}
