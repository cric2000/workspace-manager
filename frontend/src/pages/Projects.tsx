import { useEffect, useRef, useState } from 'react'
import { Plus, Search } from 'lucide-react'
import { browseProjectDirectory, inspectProject, type Report } from '../services/projects'
import { ProjectCreation } from '../components/ProjectCreation'
import { ProjectRegistry } from '../components/ProjectRegistry'
import { ProjectCreationHistory } from '../components/ProjectCreationHistory'

export function Projects() {
  const [creating, setCreating] = useState(false)
  const [history, setHistory] = useState(false)
  const [registryVersion, setRegistryVersion] = useState(0)
  const [path, setPath] = useState('')
  const [report, setReport] = useState<Report | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [choosing, setChoosing] = useState(false)
  const [selected, setSelected] = useState<number | null>(null)
  const request = useRef<AbortController | null>(null)
  const generation = useRef(0)
  useEffect(() => () => { generation.current++; request.current?.abort() }, [])

  const cancel = () => { generation.current++; request.current?.abort(); setBusy(false); setNotice('Inspection cancelled. No configuration was saved.') }
  const inspect = async () => {
    if (busy) return
    if (!path.startsWith('/') || path.includes('\0')) { setError('Enter an absolute project directory path.'); return }
    request.current?.abort()
    const controller = new AbortController(), current = ++generation.current
    request.current = controller
    setBusy(true); setError(''); setReport(null); setSelected(null); setNotice('Inspecting project metadata…')
    try {
      const result = await inspectProject(path, controller.signal)
      if (current !== generation.current) return
      if (result.code || !result.report) { setError(result.message || 'Could not inspect this directory. Try again.'); setNotice('') }
      else { setReport(result.report); setNotice(result.report.partial ? 'Inspection completed with limits or unreadable metadata. Review the findings.' : 'Inspection complete. Suggestions are not approved commands.') }
    } catch {
      if (current === generation.current) { setError('Could not connect to the project inspector. Open the native application and try again.'); setNotice('') }
    } finally { if (current === generation.current) setBusy(false) }
  }
  const candidates = report?.candidates ?? []
  const candidate = selected === null ? null : candidates[selected]

  const browse = async () => {
    if (choosing || busy) return
    const current = ++generation.current
    setChoosing(true); setError('')
    try {
      const result = await browseProjectDirectory()
      if (current !== generation.current) return
      if (result.path) { setPath(result.path); setReport(null); setSelected(null); setNotice('Directory selected. Inspect it to review startup suggestions.') }
      else if (result.code !== 'cancelled') setError(result.message || 'Could not choose a directory.')
    } catch { if (current === generation.current) setError('Could not open the native directory chooser. Enter an absolute directory path.') }
    finally { if (current === generation.current) setChoosing(false) }
  }

  return <>
    <section className="workspace-heading" aria-labelledby="projects-title"><div><p className="eyebrow">UNDERSTAND YOUR CODEBASE</p><h1 id="projects-title">Projects</h1><p className="muted">Inspect a local directory and explore its startup choices.</p></div><div className="project-command-actions"><button className="secondary-button" disabled={busy || choosing || creating || history} onClick={() => setHistory(true)}>Creation history</button><button className="primary-button" disabled={busy || choosing || history} onClick={() => setCreating(true)}><Plus size={17} aria-hidden="true" />New project</button></div></section>
    <form className="panel project-path-form" onSubmit={event => { event.preventDefault(); void inspect() }}>
      <label htmlFor="project-directory">Project directory<input id="project-directory" required placeholder="/home/you/projects/example" value={path} disabled={busy || choosing} onChange={event => { setPath(event.target.value); setReport(null); setSelected(null); setError(''); setNotice('') }} /></label>
      <div className="flex flex-wrap gap-2"><button className="secondary-button" type="button" disabled={busy || choosing} onClick={() => void browse()}>{choosing ? 'Choosing…' : 'Browse directories'}</button><button className="primary-button" type="submit" disabled={busy || choosing || !path}><Search size={15} aria-hidden="true" />{busy ? 'Inspecting…' : report ? 'Rescan directory' : 'Inspect directory'}</button>{busy && <button className="secondary-button" type="button" onClick={cancel}>Cancel inspection</button>}</div>
      <p className="muted text-xs">Paste an absolute directory path. Inspection reads bounded metadata and source files; it runs no project tools and saves no configuration.</p>
    </form>
    <p className="operation-status" role="status" aria-live="polite">{notice}</p>
    {error && <p className="error-panel" role="alert">{error}</p>}
    {report && <section aria-label="Project inspection results">
      <div className="project-report-heading"><h2>Inspection results</h2><p className="project-location">{report.path}</p><p className="muted text-xs">Point-in-time findings. Changed files require a rescan; no startup configuration has been saved.</p></div>
      {!!report.findings?.length && <section className="project-findings" aria-label="Inspection findings"><h3>Review these findings</h3><ul>{report.findings.map((finding, index) => <li key={index}>{finding.file && <code>{finding.file}: </code>}{finding.message}</li>)}</ul></section>}
      <div className="project-stack-grid">{report.stacks?.map((stack, index) => <article className="panel" key={index} aria-label={`${stack.language} detection`}><h3>{stack.language}</h3><dl className="launch-review"><div><dt>Framework evidence</dt><dd>{stack.frameworks?.join(', ') || 'Not established'}</dd></div><div><dt>Package manager evidence</dt><dd>{stack.packageManagers?.join(', ') || 'Not established'}</dd></div><div><dt>Metadata</dt><dd>{stack.evidence?.join(', ')}</dd></div><div><dt>Scripts / entry points</dt><dd>{stack.entryPoints?.length ? <ul>{stack.entryPoints.map(entry => <li key={entry}>{entry}</li>)}</ul> : 'None established'}</dd></div></dl></article>)}</div>
      <div className="project-candidate-browser"><section aria-label="Startup candidates"><h2>Startup candidates</h2><p className="muted text-xs">Each candidate is a suggestion. Select one to inspect its exact arguments.</p>{candidates.length ? <ul className="project-candidate-list">{candidates.map((item, index) => <li key={index}><button className="application-choice" aria-pressed={index === selected} onClick={() => setSelected(index)} aria-label={`Inspect candidate ${item.label}`}><span className="application-label"><strong>{item.label}</strong><span>{item.confidence} evidence</span></span></button></li>)}</ul> : <p className="picker-empty">No startup command was established. Review the findings or inspect a more specific directory.</p>}</section>
        <section className="panel" aria-label="Candidate details">{candidate ? <><h3>{candidate.label}</h3><dl className="launch-review"><div><dt>Executable tool</dt><dd><code>{candidate.program}</code></dd></div><div><dt>Literal arguments</dt><dd>{candidate.arguments?.length ? <ol>{candidate.arguments.map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol> : 'No arguments'}</dd></div><div><dt>Working directory</dt><dd>{candidate.workingDirectory}</dd></div><div><dt>Why this candidate</dt><dd>{candidate.reason}</dd></div></dl></> : <><h3>Inspect a suggestion</h3><p className="muted text-xs">Select a candidate to see its tool, argument list and the evidence behind it.</p></>}</section>
      </div>
      {!!report.tools?.length && <section aria-label="Required tools" className="panel"><h2>Required tools</h2><p className="muted text-xs">PATH presence only. Versions, plugins, environments and dependencies were not checked.</p><ul className="project-tools">{report.tools.map(tool => <li key={tool.name}><code>{tool.name}</code><span>{tool.available ? 'Found on PATH' : 'Not found on PATH'}</span></li>)}</ul></section>}
    </section>}
    <ProjectRegistry key={registryVersion} report={report} candidate={candidate} />
    {history && <ProjectCreationHistory onClose={() => setHistory(false)} onRestored={() => setRegistryVersion(value => value + 1)} />}
    {creating && <ProjectCreation onClose={() => setCreating(false)} onCreated={result => { setCreating(false); setRegistryVersion(value => value + 1); setPath(result.path); setReport(null); setSelected(null); setError(result.code ? result.message : ''); setNotice(result.registered ? 'Project created and registered. Review its startup command before execution.' : 'Project files created. Check the result and inspect the directory if registration is needed.') }} />}
    <aside className="scope-note"><h2>Inspect first. Review before saving.</h2><p>Inspection leaves saved configurations alone. Rescan a registered project to compare suggestions and explicitly choose a change for review. Readiness checks, editor and stop actions are saved configuration; execution will be added later. Saving runs nothing. Associate saved projects from Workspaces.</p></aside>
  </>
}
