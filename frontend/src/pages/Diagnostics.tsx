import { useEffect, useState } from 'react'
import { CircleAlert, Monitor, RefreshCw } from 'lucide-react'
import { getInfo, type Info } from '../services/desktop'

type Connection = { status: 'loading' } | { status: 'ready'; info: Info } | { status: 'error' }

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
          <p className="eyebrow">SYSTEM & CONNECTION</p>
          <h1 id="welcome-title">Diagnostics</h1>
          <p className="intro-copy">Check your desktop connection and application version.</p>
          <div className="connection-pill" role="status" aria-live="polite">
            <span className={`status-dot ${ready ? 'connected' : ''}`} />
            {ready ? 'Desktop bridge connected' : connection.status === 'error' ? 'Desktop bridge unavailable' : 'Connecting to desktop…'}
          </div>
        </section>

        <div className="diagnostics-content">
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

        </div>

    </>
  )
}
