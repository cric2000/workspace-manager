import type { CommandAction, CommandRuntime } from '../../bindings/workspace-manager/internal/domain/models'
import type { PreferencesDraft } from '../../bindings/workspace-manager/internal/projects/models'

function ActionFields({ label, action, onChange }: { label: string; action: CommandAction; onChange: (value: CommandAction) => void }) {
  const argumentsList = action.arguments ?? []
  return <fieldset className="project-action-fields"><legend>{label}</legend>
    <label>{label} executable or tool<input data-action-field="executable" required value={action.executable} onChange={event => onChange({ ...action, executable: event.target.value })} /></label>
    <label>{label} working directory<input required value={action.workingDirectory} onChange={event => onChange({ ...action, workingDirectory: event.target.value })} /></label>
    <p className="draft-note">Arguments are literal rows. Configure the project path as an argument when your editor needs it.</p>
    {argumentsList.map((argument, index) => <div className="argument-row" key={index}><label>{label} argument {index + 1}<input value={argument} onChange={event => onChange({ ...action, arguments: argumentsList.map((old, i) => i === index ? event.target.value : old) })} /></label><button type="button" className="secondary-button" aria-label={`Remove ${label} argument ${index + 1}`} onClick={() => onChange({ ...action, arguments: argumentsList.filter((_, i) => i !== index) })}>Remove</button></div>)}
    <button type="button" className="secondary-button" disabled={argumentsList.length >= 128} onClick={() => onChange({ ...action, arguments: [...argumentsList, ''] })}>Add {label} argument</button>
  </fieldset>
}

export function ActionReview({ action, label }: { action: CommandAction; label: string }) {
  return <section className="project-action-review" aria-label={label}><h4>{label}</h4><dl className="launch-review"><div><dt>Executable</dt><dd><code>{action.executable}</code></dd></div><div><dt>Literal arguments</dt><dd>{action.arguments?.length ? <ol>{action.arguments.map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol> : 'No arguments'}</dd></div><div><dt>Working directory</dt><dd>{action.workingDirectory}</dd></div></dl></section>
}

export function ProjectPreferenceFields({ preferences, path, onChange }: { preferences: PreferencesDraft | null | undefined; path: string; onChange: (value: PreferencesDraft) => void }) {
  const value = preferences ?? { previewUrl: '' }
  return <fieldset className="project-preferences"><legend>Preview and editor</legend>
    <label>Preview URL <span className="muted">(optional)</span><input type="url" placeholder="http://localhost:port/" value={value.previewUrl} onChange={event => onChange({ ...value, previewUrl: event.target.value })} /></label>
    <p className="draft-note">Use an explicit HTTP(S) URL without embedded credentials. No port is inferred and no URL opens or endpoint is contacted during review.</p>
    <label className="project-checkbox"><input type="checkbox" checked={!!value.editor} onChange={event => onChange({ ...value, editor: event.target.checked ? { executable: '', workingDirectory: path, arguments: [path] } : null })} />Configure an editor action</label>
    {value.editor && <ActionFields label="Editor" action={value.editor} onChange={editor => onChange({ ...value, editor })} />}
  </fieldset>
}

export function ProjectPreferenceReview({ preferences }: { preferences: { previewUrl?: string; editor?: CommandAction | null } | null | undefined }) {
  return <section aria-label="Preview and editor review"><h3>Preview and editor</h3><dl className="launch-review"><div><dt>Preview URL</dt><dd>{preferences?.previewUrl || 'Not configured'}</dd></div></dl>{preferences?.editor ? <ActionReview label="Editor action" action={preferences.editor} /> : <p className="draft-note">No editor action.</p>}</section>
}

export function CommandRuntimeFields({ runtime, path, index, onChange }: { runtime: CommandRuntime | null | undefined; path: string; index: number; onChange: (value: CommandRuntime | null) => void }) {
  const value = runtime ?? {}
  const files = value.environmentFiles ?? []
  const stop = value.stop
  const label = `Command ${index + 1}`
  return <details className="project-runtime-settings"><summary>{label} environment and startup behavior</summary>
    <label>{label} launch delay (seconds)<input name="launch-delay" type="number" min={0} max={3600} step={1} value={value.delaySeconds ?? 0} onChange={event => onChange({ ...value, delaySeconds: Number(event.target.value) })} /></label>
    <fieldset><legend>{label} environment file references</legend><p className="draft-note">Paths are relative to the project root. Order is preserved. Contents are never read or saved here; missing files may be created later.</p>
      {files.map((file, position) => <div className="argument-row" key={position}><label>{label} environment file {position + 1}<input required placeholder=".env.local" value={file} onChange={event => onChange({ ...value, environmentFiles: files.map((old, i) => i === position ? event.target.value : old) })} /></label><button className="secondary-button" type="button" aria-label={`Remove ${label} environment file ${position + 1}`} onClick={() => onChange({ ...value, environmentFiles: files.filter((_, i) => i !== position) })}>Remove</button></div>)}
      <button type="button" className="secondary-button" disabled={files.length >= 16} onClick={() => onChange({ ...value, environmentFiles: [...files, ''] })}>Add {label} environment file</button>
    </fieldset>
    <label>{label} readiness<select name="readiness-kind" value={value.readiness?.kind ?? ''} onChange={event => onChange({ ...value, readiness: event.target.value === 'http' ? { kind: 'http', url: '', expectedStatus: 200, timeoutSeconds: 30, intervalSeconds: 1 } : event.target.value === 'tcp' ? { kind: 'tcp', host: '127.0.0.1', port: 0, timeoutSeconds: 30, intervalSeconds: 1 } : null })}><option value="">No readiness check</option><option value="http">HTTP status on loopback</option><option value="tcp">TCP port on loopback</option></select></label>
    {value.readiness && <fieldset><legend>{label} readiness settings</legend>
      {value.readiness.kind === 'http' ? <><label>{label} readiness URL<input type="url" required placeholder="http://127.0.0.1:port/health" value={value.readiness.url ?? ''} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, url: event.target.value } })} /></label><label>{label} expected HTTP status<input type="number" min={200} max={599} required value={value.readiness.expectedStatus} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, expectedStatus: Number(event.target.value) } })} /></label></> : <><label>{label} readiness host<input required value={value.readiness.host ?? ''} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, host: event.target.value } })} /></label><label>{label} readiness port<input type="number" min={1} max={65535} required value={value.readiness.port ?? 0} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, port: Number(event.target.value) } })} /></label></>}
      <label>{label} readiness timeout (seconds)<input type="number" min={1} max={300} required value={value.readiness.timeoutSeconds} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, timeoutSeconds: Number(event.target.value) } })} /></label>
      <label>{label} readiness interval (seconds)<input type="number" min={1} max={30} required value={value.readiness.intervalSeconds} onChange={event => onChange({ ...value, readiness: { ...value.readiness!, intervalSeconds: Number(event.target.value) } })} /></label>
    </fieldset>}
    <label>{label} stop signal<select name="stop-signal" value={stop?.signal ?? ''} onChange={event => onChange({ ...value, stop: event.target.value ? { ...(stop ?? { graceSeconds: 10 }), signal: event.target.value } : null })}><option value="">No custom stop policy</option><option value="SIGTERM">SIGTERM</option><option value="SIGINT">SIGINT</option></select></label>
    {stop && <fieldset><legend>{label} graceful stop</legend><label>{label} stop grace (seconds)<input type="number" min={1} max={120} required value={stop.graceSeconds} onChange={event => onChange({ ...value, stop: { ...stop, graceSeconds: Number(event.target.value) } })} /></label><p className="draft-note">For managed project processes only. No automatic force stop. The optional action is configured to run before the stop signal.</p>
      <label className="project-checkbox"><input type="checkbox" checked={!!stop.action} onChange={event => onChange({ ...value, stop: { ...stop, action: event.target.checked ? { executable: '', arguments: [], workingDirectory: path } : null } })} />Configure {label} stop action</label>
      {stop.action && <ActionFields label={`${label} stop action`} action={stop.action} onChange={action => onChange({ ...value, stop: { ...stop, action } })} />}
    </fieldset>}
    <button className="secondary-button" type="button" onClick={() => onChange(null)}>Clear {label} runtime settings</button>
  </details>
}

export function CommandRuntimeReview({ runtime }: { runtime: CommandRuntime | null | undefined }) {
  const check = runtime?.readiness
  return <div className="project-runtime-review"><dl className="launch-review"><div><dt>Launch delay</dt><dd>{runtime?.delaySeconds ?? 0} seconds</dd></div><div><dt>Environment file references (project-relative)</dt><dd>{runtime?.environmentFiles?.length ? <ol>{runtime.environmentFiles.map(file => <li key={file}><code>{JSON.stringify(file)}</code></li>)}</ol> : 'None'}</dd></div><div><dt>Readiness</dt><dd>{check ? <>{check.kind === 'http' ? `HTTP ${check.expectedStatus}: ${check.url}` : `TCP ${check.host}:${check.port}`}<br />Timeout {check.timeoutSeconds}s; interval {check.intervalSeconds}s</> : 'No readiness check'}</dd></div><div><dt>Stop behavior</dt><dd>{runtime?.stop ? `${runtime.stop.signal}; ${runtime.stop.graceSeconds}s grace; no automatic force stop` : 'No custom stop policy'}</dd></div></dl>{runtime?.stop?.action && <ActionReview label="Stop action" action={runtime.stop.action} />}</div>
}
