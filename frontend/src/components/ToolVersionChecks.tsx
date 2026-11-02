import { useEffect, useRef, useState } from 'react'
import { checkToolVersions, type ToolVersionCommand, type ToolVersionResult } from '../services/projects'

export function ToolVersionChecks({ scope, token, commands, disabled, onBusyChange }: {
  scope: 'creation' | 'setup'; token: string; commands: ToolVersionCommand[]; disabled: boolean; onBusyChange: (busy: boolean) => void
}) {
  const [result, setResult] = useState<ToolVersionResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const request = useRef<AbortController | null>(null)
  useEffect(() => { onBusyChange(busy) }, [busy, onBusyChange])
  useEffect(() => () => { request.current?.abort(); onBusyChange(false) }, [onBusyChange])
  async function check() {
    if (request.current || disabled) return
    const controller = new AbortController()
    request.current = controller
    setBusy(true); setError(''); setResult(null)
    try { setResult(await checkToolVersions(scope, token, controller.signal)) }
    catch { setError(controller.signal.aborted ? 'Version checks cancelled. No project setup was requested.' : 'Version results unavailable. You can request the checks again; no project setup was requested.') }
    finally { request.current = null; setBusy(false) }
  }
  return <section aria-label="Tool version checks">
    <h3>Tool version checks</h3>
    <p className="draft-note">Optional diagnostics run only the commands below in a private temporary directory. No downloads or project setup are requested. Each command has a three-second limit. Results are advisory and do not approve execution.</p>
    <details><summary>Review version commands</summary><ul>{commands.map(command => <li key={command.tool}><code>{command.executable} {(command.arguments ?? []).map(argument => JSON.stringify(argument)).join(' ')}</code> · {command.minimumVersion ? `minimum ${command.minimumVersion}` : 'version detection only'}</li>)}</ul></details>
    <div className="dialog-actions"><button type="button" className="secondary-button" disabled={busy || disabled} onClick={() => void check()}>{busy ? 'Checking tool versions…' : 'Check tool versions'}</button>{busy && <button type="button" className="secondary-button" onClick={() => request.current?.abort()}>Cancel version checks</button>}</div>
    {result && <div role="status"><p>{result.message}</p><ul>{result.checks?.map(check => <li key={check.tool}><strong>{check.tool}</strong>: {check.version || 'version unconfirmed'}{check.minimumVersion ? ` (minimum ${check.minimumVersion})` : ''} · {check.status}. {check.message}</li>)}</ul></div>}
    {error && <p role="alert" className="error-panel">{error}</p>}
  </section>
}
