import { useEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { CommandRuntimeFields, CommandRuntimeReview, ProjectPreferenceFields, ProjectPreferenceReview } from './ProjectSettings'
import { ProjectRemoval } from './ProjectRemoval'
import { ProjectRescan } from './ProjectRescan'
import { projectDraft } from '../lib/projectDraft'
import { commitRegistration, listProjects, reviewRegistration, type Candidate, type CommandDraft, type ProjectView, type RegistrationDraft, type RegistrationReview, type RegistryResult, type RegistryState, type Report } from '../services/projects'

export function ProjectRegistry({ report, candidate }: { report: Report | null; candidate: Candidate | undefined | null }) {
  const [state, setState] = useState<RegistryState | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [draft, setDraft] = useState<RegistrationDraft | null>(null)
  const [comparison, setComparison] = useState<ProjectView | null>(null)
  const [removal, setRemoval] = useState<ProjectView | null>(null)
  const alive = useRef(true)
  const request = useRef(0)
  const reload = async () => {
    const current = ++request.current
    setLoading(true); setError('')
    try {
      const result = await listProjects()
      if (!alive.current || current !== request.current) return
      if (result.code || !result.state) { setState(null); setError(result.message || 'Could not load registered projects.') }
      else setState(result.state)
    } catch { if (alive.current && current === request.current) { setState(null); setError('Could not connect to project storage. Reload registered projects to try again.') } }
    finally { if (alive.current && current === request.current) setLoading(false) }
  }
  useEffect(() => { alive.current = true; void reload(); return () => { alive.current = false; request.current++ } }, [])
  const edit = (view: ProjectView) => setDraft(projectDraft(view))
  const saved = (result: RegistryResult) => {
    request.current++ // A pending list must not replace the saved result.
    setLoading(false); setDraft(null); setState(result.state); setError(result.message)
    setNotice('Project configuration saved. No command was started.')
  }
  return <section className="project-library" aria-label="Registered projects">
    <div className="project-library-heading"><div><h2>Registered projects</h2><p className="muted text-xs">Saved startup configurations. Commands are not run from this screen.</p></div><button className="secondary-button" disabled={loading || !!draft || !!comparison || !!removal} onClick={() => void reload()}>Reload registered projects</button></div>
    {report && <div className="panel project-import-action"><p>{candidate ? `Configure “${candidate.label}” for this project, or edit its commands before review.` : 'Register this directory without a startup command, or add a custom command before review.'}</p><button className="primary-button" disabled={!state || loading || !!removal || !!comparison || !!draft} onClick={() => setDraft({ id: '', revision: '', name: report.path.split('/').pop() || '', path: report.path, commands: candidate ? [{ id: '', configuration: { name: candidate.label, executable: candidate.program, arguments: [...(candidate.arguments ?? [])], workingDirectory: candidate.workingDirectory } }] : [] })}>Configure project</button></div>}
    {notice && <p className="draft-note" aria-live="polite">{notice}</p>}
    {loading && <p className="draft-note">Loading registered projects…</p>}
    {error && <p className="error-panel" role="alert">{error}</p>}
    {state && !state.projects?.length && <p className="picker-empty">No registered projects. Inspect a directory to configure it.</p>}
    <div className="project-stack-grid">{state?.projects?.map(view => <article className="panel" key={view.project.id} aria-label={`Registered project ${view.project.name}`}><h3>{view.project.name}</h3><p className="project-location">{view.project.path}</p><p className="muted text-xs">{view.project.commands?.length || 0} startup commands</p><ul className="project-command-summary">{view.project.commands?.map(command => <li key={command.id}>{command.configuration.name} · {command.approval ? 'Configuration approved' : 'Review required'}</li>)}</ul><div className="project-command-actions"><button className="secondary-button" disabled={loading || !!draft || !!comparison || !!removal} onClick={() => edit(view)}>Edit project {view.project.name}</button><button className="secondary-button project-rescan-button" disabled={loading || !!draft || !!comparison || !!removal} onClick={() => setComparison(view)}>Rescan project {view.project.name}</button><button className="secondary-button delete-button" disabled={loading || !!draft || !!comparison || !!removal} onClick={() => setRemoval(view)}>Remove project {view.project.name}</button></div></article>)}</div>
    {removal && <ProjectRemoval view={removal} onClose={() => setRemoval(null)} onRemoved={result => { request.current++; setRemoval(null); setState(result.state); setError(result.message); setNotice('Project registration removed. Project files are unchanged.') }} />}
    {comparison && <ProjectRescan view={comparison} onClose={() => setComparison(null)} onUse={value => { setComparison(null); setDraft(value) }} />}
    {draft && <RegistrationEditor initial={draft} onClose={() => setDraft(null)} onSaved={saved} />}
  </section>
}

function RegistrationEditor({ initial, onClose, onSaved }: { initial: RegistrationDraft; onClose: () => void; onSaved: (result: RegistryResult) => void }) {
  const [draft, setDraft] = useState(initial)
  const [review, setReview] = useState<RegistrationReview | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const reviewHeading = useRef<HTMLHeadingElement>(null)
  const commands = draft.commands ?? []
  const working = useRef(false)
  useEffect(() => {
    if (review) reviewHeading.current?.focus()
    else document.querySelector<HTMLInputElement>('#registration-name')?.focus()
  }, [review])
  const editCommand = (index: number, command: CommandDraft) => setDraft({ ...draft, commands: commands.map((old, i) => i === index ? command : old) })
  const reviewDraft = async () => {
    if (working.current) return
    working.current = true; setBusy(true); setError('')
    try {
      const result = await reviewRegistration(draft)
      if (result.code || !result.review) { setError(result.message || 'Could not prepare a review.'); setBlocked(result.code === 'conflict' || result.code === 'not_found') }
      else setReview(result.review)
    } catch { setError('Could not prepare the review. No configuration was saved. Try again.') }
    finally { working.current = false; setBusy(false) }
  }
  const save = async () => {
    if (!review || working.current) return
    working.current = true; setBusy(true); setError('')
    try {
      const result = await commitRegistration(review.token)
      if (result.committed) { onSaved(result); return }
      if (uncertain && result.code === 'review_changed') {
        setUncertain(false); setReview(null); setBlocked(true)
        setError('The saved outcome is no longer available for this review. Close this editor and reload registered projects before making another change.')
        return
      }
      setUncertain(false); setReview(null); setError(result.message || 'Configuration was not saved. Review again.'); setBlocked(result.code === 'conflict' || result.code === 'not_found')
    } catch { setUncertain(true); setError('The save response was lost. Check this same review to retrieve its outcome, or close and reload registered projects before editing again.') }
    finally { working.current = false; setBusy(false) }
  }
  return <Modal title={review ? 'Review project registration' : initial.id ? 'Edit project configuration' : 'Configure project'} description={review ? 'Approve these exact startup configurations for future managed execution. Saving starts nothing.' : 'Configure literal arguments. Each command requires a fresh review before saving.'} busy={busy} onClose={onClose} wide>
    {!review ? <form className="editor-fields" onSubmit={event => { event.preventDefault(); void reviewDraft() }}>
      <fieldset disabled={busy || blocked}>
        <label htmlFor="registration-name">Project name<input data-dialog-initial-focus id="registration-name" required maxLength={120} value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} /></label>
        <label htmlFor="registration-path">Canonical project directory<input id="registration-path" readOnly value={draft.path} /></label>
        <p className="draft-note">Project contents remain in place. Working directories must be inside this project. Tools on PATH resolve to an absolute executable during review.</p>
        <ProjectPreferenceFields preferences={draft.preferences} path={draft.path} onChange={preferences => setDraft({ ...draft, preferences })} />
        {commands.map((command, index) => <fieldset className="project-command-editor" key={index}><legend>Startup command {index + 1}</legend>
          <label>Name<input required maxLength={120} value={command.configuration.name} onChange={event => editCommand(index, { ...command, configuration: { ...command.configuration, name: event.target.value } })} /></label>
          <label>Executable or tool<input required placeholder="go or /absolute/path/to/tool" value={command.configuration.executable} onChange={event => editCommand(index, { ...command, configuration: { ...command.configuration, executable: event.target.value } })} /></label>
          <label>Working directory<input required value={command.configuration.workingDirectory} onChange={event => editCommand(index, { ...command, configuration: { ...command.configuration, workingDirectory: event.target.value } })} /></label>
          <fieldset><legend>Literal arguments</legend><p className="muted text-xs">One row per argument. Empty rows pass an empty argument. Shell syntax is literal unless you explicitly configure a shell executable.</p>
            {(command.configuration.arguments ?? []).map((argument, position) => <div className="argument-row" key={position}><label>Command {index + 1} argument {position + 1}<input value={argument} onChange={event => editCommand(index, { ...command, configuration: { ...command.configuration, arguments: command.configuration.arguments!.map((old, i) => i === position ? event.target.value : old) } })} /></label><button className="secondary-button" type="button" aria-label={`Remove command ${index + 1} argument ${position + 1}`} onClick={() => editCommand(index, { ...command, configuration: { ...command.configuration, arguments: command.configuration.arguments!.filter((_, i) => i !== position) } })}>Remove</button></div>)}
            <button className="secondary-button" type="button" disabled={(command.configuration.arguments?.length ?? 0) >= 128} onClick={() => editCommand(index, { ...command, configuration: { ...command.configuration, arguments: [...(command.configuration.arguments ?? []), ''] } })}>Add argument to command {index + 1}</button>
          </fieldset>
          <CommandRuntimeFields runtime={command.configuration.runtime} path={draft.path} index={index} onChange={runtime => editCommand(index, { ...command, configuration: { ...command.configuration, runtime } })} />
          <div className="project-command-actions"><button type="button" className="secondary-button" disabled={index === 0} onClick={() => { const reordered = [...commands]; [reordered[index - 1], reordered[index]] = [reordered[index], reordered[index - 1]]; setDraft({ ...draft, commands: reordered }) }}>Move command {index + 1} up</button><button type="button" className="secondary-button" onClick={() => setDraft({ ...draft, commands: commands.filter((_, i) => i !== index) })}>Remove command {index + 1}</button></div>
        </fieldset>)}
        <button className="secondary-button" type="button" disabled={commands.length >= 16} onClick={() => setDraft({ ...draft, commands: [...commands, { id: '', configuration: { name: '', executable: '', workingDirectory: draft.path, arguments: [] } }] })}>Add startup command</button>
        {!commands.length && <p className="draft-note">This project will be registered without startup commands.</p>}
      </fieldset>
      {error && <p className="error-panel" role="alert">{error}</p>}
      {blocked && <p className="draft-note">Close this editor and reload registered projects. Your draft above is retained for reference until you close.</p>}
      <div className="dialog-actions"><button className="secondary-button" type="button" disabled={busy} onClick={onClose}>Cancel</button><button className="primary-button" type="submit" disabled={busy || blocked}>{busy ? 'Preparing review…' : 'Review configuration'}</button></div>
    </form> : <>
      <div aria-label="Registration review"><h3 ref={reviewHeading} tabIndex={-1}>{review.project.name}</h3><p className="project-location">{review.project.path}</p><ProjectPreferenceReview preferences={review.project.preferences} />{review.project.commands?.map((command, index) => <section className="project-command-editor" key={command.id}><h3>{index + 1}. {command.configuration.name}</h3><dl className="launch-review"><div><dt>Executable</dt><dd><code>{command.configuration.executable}</code></dd></div><div><dt>Literal arguments</dt><dd>{command.configuration.arguments?.length ? <ol>{command.configuration.arguments.map((argument, i) => <li key={i}><code>{JSON.stringify(argument)}</code></li>)}</ol> : 'No arguments'}</dd></div><div><dt>Working directory</dt><dd>{command.configuration.workingDirectory}</dd></div></dl><CommandRuntimeReview runtime={command.configuration.runtime} /></section>)}{!review.project.commands?.length && <p className="draft-note">No startup commands.</p>}<p className="draft-note">Approval covers the configuration shown. Project source, scripts and dependencies can change afterward. No environment file contents are read. No readiness check, editor action, stop action, dependency installation, tool version check or execution occurs here. Review expires at {new Date(review.expiresAt).toLocaleTimeString()}.</p></div>
      {error && <p className="error-panel" role="alert">{error}</p>}
      <div className="dialog-actions"><button className="secondary-button" disabled={busy || uncertain} onClick={() => { setReview(null); setError('') }}>Back to configuration</button><button className="primary-button" disabled={busy} onClick={() => void save()}>{busy ? 'Saving…' : uncertain ? 'Check save outcome' : 'Approve and save project'}</button></div>
    </>}
  </Modal>
}
