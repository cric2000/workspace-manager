import { useEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { CommandRuntimeReview, ProjectPreferenceReview } from './ProjectSettings'
import { candidateDraft } from '../lib/projectDraft'
import { rescanProject, type ProjectComparison, type ProjectView, type RegistrationDraft } from '../services/projects'

export function ProjectRescan({ view, onClose, onUse }: { view: ProjectView; onClose: () => void; onUse: (draft: RegistrationDraft) => void }) {
  const [comparison, setComparison] = useState<ProjectComparison | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [selected, setSelected] = useState('')
  const [target, setTarget] = useState('')
  const generation = useRef(0)
  const request = useRef<AbortController | null>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  const scan = async () => {
    request.current?.abort()
    const current = ++generation.current, controller = new AbortController()
    request.current = controller
    setBusy(true); setComparison(null); setSelected(''); setTarget(''); setError(''); setNotice('Rescanning project metadata…')
    try {
      const result = await rescanProject({ id: view.project.id, revision: view.revision }, controller.signal)
      if (current !== generation.current) return
      if (result.code || !result.comparison) { setError(result.message || 'Could not compare this rescan. Close and reload registered projects.'); setNotice('') }
      else { setComparison(result.comparison); setNotice('Rescan complete. Saved configuration is unchanged.') }
    } catch { if (current === generation.current) { setError('Could not connect to the project inspector. Retry the rescan or close this comparison.'); setNotice('') } }
    finally { if (current === generation.current) setBusy(false) }
  }
  useEffect(() => {
    void scan()
    return () => { generation.current++; request.current?.abort() }
  }, [])
  useEffect(() => { if (comparison) heading.current?.focus() }, [comparison])
  const cancel = () => { generation.current++; request.current?.abort(); setBusy(false); setNotice('Rescan cancelled. Saved configuration is unchanged.') }
  const candidateIndex = selected === '' ? -1 : Number(selected)
  const candidate = comparison?.report.candidates?.[candidateIndex]
  const status = comparison?.candidates?.[candidateIndex]
  const savedCommands = comparison?.saved.project.commands ?? []
  const saved = savedCommands.find(command => command.id === target)
  const matched = new Set(comparison?.candidates?.flatMap(item => item.matchingCommandIds ?? []) ?? [])
  const changed = candidate && saved ? {
    executable: (status?.resolvedExecutable || candidate.program) !== saved.configuration.executable,
    arguments: JSON.stringify(candidate.arguments ?? []) !== JSON.stringify(saved.configuration.arguments ?? []),
    workingDirectory: candidate.workingDirectory !== saved.configuration.workingDirectory,
  } : null
  const useCandidate = () => {
    if (!candidate || !comparison || !target) return
    const draft = candidateDraft(comparison.saved, candidate, target)
    if (draft) onUse(draft)
  }
  return <Modal title={`Rescan ${view.project.name}`} description="Compare saved commands with current suggestions. Choose a change explicitly, then review the complete draft before saving." busy={false} onClose={onClose} wide>
    <p className="project-location">{view.project.path}</p>
    <p role="status" className="draft-note" aria-live="polite">{notice}</p>
    {error && <p role="alert" className="error-panel">{error}</p>}
    <div className="project-command-actions"><button className="secondary-button" disabled={busy} onClick={() => void scan()}>Rescan comparison</button>{busy && <button className="secondary-button" onClick={cancel}>Cancel rescan</button>}</div>
    {comparison && <>
      <h3 ref={heading} tabIndex={-1} className="project-comparison-heading">Saved commands and current suggestions</h3>
      <p className="draft-note">Point-in-time findings. Matching invocation fields do not establish unchanged project code. A missing suggestion does not establish that a saved command should be removed.</p>
      {comparison.report.partial && <p className="error-panel">This scan is partial. Limits or unreadable metadata may hide candidates.</p>}
      {!!comparison.report.findings?.length && <section aria-label="Rescan findings" className="project-findings"><h4>Inspection findings</h4><ul>{comparison.report.findings.map((finding, index) => <li key={index}>{finding.file && <code>{finding.file}: </code>}{finding.message}</li>)}</ul></section>}
      <ul className="project-command-summary" aria-label="Saved command matches">{savedCommands.map(command => <li key={command.id}>{command.configuration.name}: {matched.has(command.id) ? 'Equivalent invocation detected' : 'No equivalent invocation established'}</li>)}</ul>
      {!savedCommands.length && <p className="draft-note">No saved startup commands.</p>}
      {comparison.report.candidates?.length ? <div className="editor-fields">
        <label>Detected candidate<select name="rescan-candidate" data-dialog-initial-focus value={selected} onChange={event => setSelected(event.target.value)}><option value="">Choose a candidate</option>{comparison.report.candidates.map((item, index) => <option key={index} value={index}>{item.label}</option>)}</select></label>
        {candidate && <><p className="draft-note">{candidate.reason} ({candidate.confidence} evidence)</p><p className="draft-note">{status?.available ? `Resolved executable: ${status.resolvedExecutable}` : 'Executable is unavailable or could not be resolved. Edit the draft to choose an available tool before saving.'}</p>
          <label>Apply candidate to<select name="rescan-target" value={target} onChange={event => setTarget(event.target.value)}><option value="">Choose a saved command or add a command</option><option value="append" disabled={savedCommands.length >= 16}>Add a new startup command{savedCommands.length >= 16 ? ' (16-command limit)' : ''}</option>{savedCommands.map(command => <option value={command.id} key={command.id}>Replace invocation: {command.configuration.name}</option>)}</select></label>
          {target && <section aria-label="Candidate change comparison" className="project-comparison">
            <table><caption>{saved ? `Invocation changes for ${saved.configuration.name}` : `Add ${candidate.label}`}</caption><thead><tr><th scope="col">Field</th><th scope="col">Saved</th><th scope="col">Proposed</th></tr></thead><tbody>
              <tr><th scope="row">Executable{changed?.executable ? ' (changed)' : ''}</th><td><code>{saved?.configuration.executable ?? 'New command'}</code></td><td><code>{status?.resolvedExecutable || candidate.program}</code><p className="muted text-xs">Requested tool: {candidate.program}</p></td></tr>
              <tr><th scope="row">Literal arguments{changed?.arguments ? ' (changed)' : ''}</th><td><Arguments values={saved?.configuration.arguments} /></td><td><Arguments values={candidate.arguments} /></td></tr>
              <tr><th scope="row">Working directory{changed?.workingDirectory ? ' (changed)' : ''}</th><td>{saved?.configuration.workingDirectory ?? 'New command'}</td><td>{candidate.workingDirectory}</td></tr>
            </tbody></table>
            {saved ? <><p className="draft-note">Command name, order, environment references, readiness, stop and delay settings are preserved. Other commands and project preferences are preserved. The changed invocation requires fresh review.</p><CommandRuntimeReview runtime={saved.configuration.runtime} /></> : <p className="draft-note">Adds one command after the existing commands, with no runtime settings. Existing commands and project preferences are preserved.</p>}
          </section>}
        </>}
      </div> : <p className="picker-empty">No startup candidates were established. Saved commands are kept. Close this comparison to edit configuration manually.</p>}
      <details className="project-runtime-settings"><summary>Preserved preview and editor settings</summary><ProjectPreferenceReview preferences={comparison.saved.project.preferences} /></details>
    </>}
    <div className="dialog-actions"><button className="secondary-button" onClick={onClose}>Close comparison</button><button className="primary-button" disabled={busy || !candidate || !target} onClick={useCandidate}>Use candidate in draft</button></div>
  </Modal>
}

function Arguments({ values }: { values: string[] | null | undefined }) {
  return values?.length ? <ol>{values.map((value, index) => <li key={index}><code>{JSON.stringify(value)}</code></li>)}</ol> : <>No arguments</>
}
