import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { Modal } from './Modal'
import { ProjectSetup } from './ProjectSetup'
import { ToolVersionChecks } from './ToolVersionChecks'
import { CommandRuntimeReview } from './ProjectSettings'
import { browseProjectDirectory, commitCreation, getCreationOutcome, prepareCreation, getPreparationOutcome, listTemplates, reviewCreation, type CreationDraft, type CreationHistoryStatus, type CreationResult, type CreationReview, type TemplateDefinition } from '../services/projects'

export function ProjectCreation({ onClose, onCreated }: { onClose: () => void; onCreated: (result: CreationResult) => void }) {
  const [draft, setDraft] = useState<CreationDraft>({ templateId: 'go', name: '', parentDirectory: '', modulePath: '' })
  const [templates, setTemplates] = useState<TemplateDefinition[]>([])
  const [loading, setLoading] = useState(true)
  const [attempt, setAttempt] = useState(0)
  const [review, setReview] = useState<CreationReview | null>(null)
  const [outcome, setOutcome] = useState<CreationResult | null>(null)
  const [busy, setBusy] = useState(false)
  const [setupBusy, setSetupBusy] = useState(false)
  const [versionsBusy, setVersionsBusy] = useState(false)
  const [historyStatus, setHistoryStatus] = useState<CreationHistoryStatus | null>(null)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const [blocked, setBlocked] = useState(false)
  const working = useRef(false)
  const creationRequest = useRef<AbortController | null>(null)
  const heading = useRef<HTMLHeadingElement>(null)
  useEffect(() => () => creationRequest.current?.abort(), [])
  useEffect(() => {
    let current = true
    setLoading(true); setError('')
    listTemplates().then(value => { if (current) { setTemplates(value ?? []); setLoading(false) } }, () => { if (current) { setLoading(false); setError('Could not load project templates. Try loading them again.') } })
    return () => { current = false }
  }, [attempt])
  useLayoutEffect(() => { if (review || outcome) heading.current?.focus(); else if (!loading) document.getElementById('creation-name')?.focus() }, [review, outcome, loading])
  async function browse() {
    if (working.current) return
    working.current = true; setBusy(true); setError('')
    try {
      const result = await browseProjectDirectory()
      if (result.path) setDraft(value => ({ ...value, parentDirectory: result.path }))
      else if (result.code !== 'cancelled') setError(result.message || 'Could not choose a parent directory.')
    } catch { setError('Could not open the native chooser. Enter an absolute parent directory path.') }
    finally { working.current = false; setBusy(false) }
  }
  async function preview() {
    if (working.current || blocked) return
    working.current = true; setBusy(true); setError('')
    try {
      const result = await reviewCreation(draft)
      if (result.code || !result.review) setError(result.message || 'Could not prepare creation. Check the options and try again.')
      else { setReview(result.review); setHistoryStatus(null) }
    } catch { setError('Could not prepare the preview. No project was created. Try again.') }
    finally { working.current = false; setBusy(false) }
  }
  const needsPreparation = !!review?.scaffold && !review.prepared
  async function create() {
    if (!review || working.current || blocked || versionsBusy) return
    working.current = true; setBusy(true); setError('')
    const controller = new AbortController()
    creationRequest.current = controller
    try {
      if (needsPreparation) {
        const result = uncertain ? await getPreparationOutcome(review.token) : await prepareCreation(review.token, controller.signal)
        if (result.history) setHistoryStatus(result.history)
        if (result.review) { setReview(result.review); setUncertain(false); return }
        if (result.code === 'preparation_pending') { setUncertain(true); setError(result.message); return }
        if (result.code === 'busy') { setError(result.message); return }
        setUncertain(false); setReview(null); setError(result.message || 'Preparation failed. No project was created. Start a new review.'); return
      }
      const result = uncertain ? await getCreationOutcome(review.token) : await commitCreation(review.token, controller.signal)
      if (result.history) setHistoryStatus(result.history)
      if (result.created) { setOutcome(result); setUncertain(false); return }
      if (uncertain && result.code === 'creation_pending') { setError(result.message); return }
      if (uncertain && result.code === 'review_changed') {
        setBlocked(true); setError('The creation outcome is no longer available. Close this dialog, check the destination and reload registered projects before creating again.'); return
      }
      setUncertain(false); setReview(null)
      setError((result.message || 'The project was not created. Review the options again.') + (result.recoveryPath ? ` Recovery path: ${result.recoveryPath}` : ''))
      if (result.recoveryPath) setBlocked(true)
    } catch { setUncertain(true); setError(needsPreparation ? 'Preparation response unavailable. No project was published. Check this review to retrieve the outcome before preparing again.' : controller.signal.aborted ? 'Cancellation requested. Files may already have been published. Check this same review to retrieve the outcome before creating again.' : 'The creation response was lost. Check this same review to retrieve its outcome, or close and check the destination and registered projects before creating again.') }
    finally { creationRequest.current = null; working.current = false; setBusy(false) }
  }
  const template = templates.find(item => item.id === draft.templateId)
  return <Modal title={outcome ? 'Project created' : review ? 'Review project creation' : 'New project'} description={outcome ? 'Project files stay in their new directory. Review the result before continuing.' : needsPreparation ? 'Review the official tool and downloads before preparing files in a private temporary directory.' : review ? 'Confirm the exact files below. No external initialization command will run.' : 'Create a starter in a new directory. Existing directories and files are kept.'} busy={busy || setupBusy || versionsBusy} onClose={() => outcome ? onCreated(outcome) : onClose()} wide>
    {historyStatus && (historyStatus.saved ? <p className="draft-note" role="status">Attempt saved in Creation history on the Projects page.</p> : <p className="error-panel" role="alert">{historyStatus.message}</p>)}
    {outcome ? <>
      <h3 ref={heading} tabIndex={-1}>{outcome.registered ? 'Created and registered' : 'Project files created'}</h3><p className="project-location">{outcome.path}</p>
      {outcome.code && <p className="error-panel" role="alert">{outcome.message}</p>}
      <ol className="project-creation-log" aria-label="Creation activity">{outcome.logs?.map((line, index) => <li key={index}>{line}</li>)}</ol>
      <p className="draft-note">No project code was executed during file creation. The saved startup command requires review and approval from Edit project. Add the registration to a workspace from Workspaces.</p>
      {review?.dependencySetup && <DependencySetup review={review} created />}
      {review && outcome.registered && <ProjectSetup creationToken={review.token} templateId={review.plan.templateId} onBusyChange={setSetupBusy} />}
      <div className="dialog-actions"><button className="primary-button" disabled={setupBusy} onClick={() => onCreated(outcome)}>Done</button></div>
    </> : review ? <>
      <h3 ref={heading} tabIndex={-1}>Create {draft.name}</h3><p className="project-location">{review.path}</p>
      {review.scaffold && <ScaffoldReview review={review} />}{!needsPreparation && <section aria-label="Generated files" className="project-creation-files">{review.plan.files?.map((file, index) => <details key={file.path} open={index === 0}><summary>{file.path}</summary><p className="draft-note">{file.encoding === 'base64' ? 'Binary asset · exact bytes shown as base64; decoded when published.' : null}</p><pre><code>{file.contents}</code></pre></details>)}</section>}
      <section aria-label="Creation commands"><h3>Initialization commands</h3><p className="draft-note">{review.scaffold ? 'The reviewed scaffolder prepares files separately. Publishing will not run it again, install dependencies or initialize Git.' : 'None. Files are generated locally without downloads, dependency installation or Git initialization.'}</p></section>
      {review.dependencySetup && <DependencySetup review={review} />}
      <section aria-label="Suggested startup command"><h3>Suggested startup command · review required</h3><dl className="launch-review"><div><dt>Executable</dt><dd><code>{review.startup.executable}</code></dd></div><div><dt>Literal arguments</dt><dd><ol>{review.startup.arguments?.map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div><div><dt>Working directory</dt><dd>{review.startup.workingDirectory}</dd></div></dl>{review.startup.runtime && <CommandRuntimeReview runtime={review.startup.runtime} />}{review.plan.previewUrl && <p className="draft-note">Saved preview URL: <code>{review.plan.previewUrl}</code>. No readiness request or browser launch occurs during creation.</p>}{review.plan.startupProjectExecutable && <p className="draft-note">Complete the manual setup steps before reviewing and approving this project-local executable.</p>}<p className="draft-note">{review.toolRequirement} {review.plan.preview} Creation does not grant execution approval. This review expires at {new Date(review.expiresAt).toLocaleTimeString()}.</p></section>
      {!!review.toolChecks?.length && <ToolVersionChecks key={review.token} scope="creation" token={review.token} commands={review.toolChecks} disabled={busy || uncertain || blocked} onBusyChange={setVersionsBusy} />}
      {error && <p className="error-panel" role="alert">{error}</p>}
      <div className="dialog-actions"><button className="secondary-button" disabled={busy || uncertain || blocked || versionsBusy} onClick={() => { setReview(null); setError('') }}>Back to options</button>{busy && !uncertain && <button className="secondary-button" onClick={() => creationRequest.current?.abort()}>{needsPreparation ? 'Cancel preparation' : 'Cancel creation'}</button>}<button className="primary-button" disabled={busy || blocked || versionsBusy} onClick={() => void create()}>{busy ? uncertain ? 'Checking…' : needsPreparation ? 'Preparing Next.js files…' : 'Creating…' : uncertain ? needsPreparation ? 'Check preparation outcome' : 'Check creation outcome' : needsPreparation ? 'Download and prepare files' : 'Confirm and create project'}</button></div>
    </> : <form className="editor-fields" onSubmit={event => { event.preventDefault(); void preview() }}>
      <fieldset disabled={busy || blocked || loading}>
        <label htmlFor="creation-template">Template<select id="creation-template" value={draft.templateId} onChange={event => { const { next: previous, ...base } = draft; setDraft({ ...base, templateId: event.target.value, modulePath: event.target.value === 'go' ? draft.modulePath : '', ...(event.target.value === 'nextjs' ? { next: previous ?? { typeScript: true, tailwind: true, appRouter: true, sourceDirectory: true, packageManager: 'npm' } } : {}) }) }}>{templates.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        {template && <p className="draft-note">{template.description} {template.requirements || `Requires ${(template.requiredTools ?? []).join(', ')} ${template.minimumVersion} or later.`}</p>}
        <label htmlFor="creation-name">Project directory name<input id="creation-name" data-dialog-initial-focus required maxLength={64} aria-describedby={template?.nameHint ? 'creation-name-hint' : undefined} value={draft.name} onChange={event => setDraft({ ...draft, name: event.target.value })} placeholder={draft.templateId === 'go' ? 'hello-go' : draft.templateId === 'python' ? 'hello-python' : draft.templateId === 'fastapi' ? 'hello-api' : draft.templateId === 'nextjs' ? 'hello-next' : 'hello-node'} /></label>
        {template?.nameHint && <p id="creation-name-hint" className="muted text-xs">{template.nameHint}</p>}
        <label htmlFor="creation-parent">Parent directory<input id="creation-parent" required value={draft.parentDirectory} onChange={event => setDraft({ ...draft, parentDirectory: event.target.value })} placeholder="/home/you/projects" /></label>
        <button className="secondary-button" type="button" onClick={() => void browse()}>Choose parent directory</button>
        {draft.templateId === 'nextjs' && draft.next && <fieldset><legend>Next.js options</legend>
          <label className="project-checkbox"><input type="checkbox" checked={draft.next.typeScript} onChange={event => setDraft({ ...draft, next: { ...draft.next!, typeScript: event.target.checked } })} />TypeScript</label>
          <label className="project-checkbox"><input type="checkbox" checked={draft.next.tailwind} onChange={event => setDraft({ ...draft, next: { ...draft.next!, tailwind: event.target.checked } })} />Tailwind CSS</label>
          <label className="project-checkbox"><input type="checkbox" checked={draft.next.appRouter} onChange={event => setDraft({ ...draft, next: { ...draft.next!, appRouter: event.target.checked } })} />App Router</label>
          <label className="project-checkbox"><input type="checkbox" checked={draft.next.sourceDirectory} onChange={event => setDraft({ ...draft, next: { ...draft.next!, sourceDirectory: event.target.checked } })} />Use src directory</label>
          <label>Package manager<select value={draft.next.packageManager} onChange={event => setDraft({ ...draft, next: { ...draft.next!, packageManager: event.target.value } })}><option value="npm">npm</option><option value="pnpm">pnpm</option></select></label>
        </fieldset>}
        {template?.options?.includes('modulePath') && <label htmlFor="creation-module">Go module path<input id="creation-module" required maxLength={512} value={draft.modulePath} onChange={event => setDraft({ ...draft, modulePath: event.target.value })} placeholder="example.com/you/hello-go" /></label>}
      </fieldset>
      {loading && <p role="status">Loading templates…</p>}
      {!loading && !templates.length && <button className="secondary-button" type="button" onClick={() => setAttempt(attempt + 1)}>Reload templates</button>}
      {error && <p className="error-panel" role="alert">{error}</p>}
      <div className="dialog-actions"><button className="secondary-button" type="button" disabled={busy} onClick={onClose}>Cancel</button><button className="primary-button" type="submit" disabled={busy || blocked || loading || !template}>{busy ? 'Preparing preview…' : 'Preview files and commands'}</button></div>
    </form>}
  </Modal>
}

function DependencySetup({ review, created = false }: { review: CreationReview; created?: boolean }) {
  const steps = review.dependencySetup ? [{ action: review.dependencySetup, plan: review.plan.dependencySetup }, ...(review.additionalSetup ?? []).map((action, index) => ({ action, plan: review.plan.additionalSetup?.[index] }))] : []
  return <>{steps.map(({ action: setup, plan }, index) => {
    const label = plan?.label || 'Dependency setup'
    return <section key={index} aria-label={`Separate ${label.toLocaleLowerCase()}`}><h3>{label} · {created ? 'manual alternative' : 'not run'}</h3>
      <p className="draft-note">{plan?.note} No packages will be installed by confirming creation.</p>
      <dl className="launch-review"><div><dt>Executable</dt><dd><code>{setup.executable}</code></dd></div><div><dt>Literal arguments</dt><dd><ol>{setup.arguments?.map((argument, i) => <li key={i}><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div><div><dt>Working directory</dt><dd>{setup.workingDirectory}</dd></div></dl>
    </section>
  })}</>
}

function ScaffoldReview({ review }: { review: CreationReview }) {
  const action = review.scaffold!
  return <section aria-label="Official scaffolder review"><h3>Official Next.js scaffolder · {review.prepared ? 'prepared privately' : 'not run'}</h3>
    <p className="draft-note">{review.plan.scaffold?.note}</p>
    <dl className="launch-review"><div><dt>Executable</dt><dd><code>{action.executable}</code></dd></div><div><dt>Literal arguments</dt><dd><ol>{action.arguments?.map((argument, index) => <li key={index}><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div><div><dt>Private working directory</dt><dd>{action.workingDirectory}</dd></div></dl>
    <p className="draft-note">{review.prepared ? 'The temporary output was captured and cleaned up. Confirming creation publishes only the file bytes shown below.' : 'Preparing files downloads and runs this official tool. It does not create the destination, install project dependencies or start a server. You will review its generated files before a separate creation confirmation.'}</p>
  </section>
}
