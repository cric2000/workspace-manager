import type { Candidate, ProjectView, RegistrationDraft } from '../services/projects'

export function projectDraft(view: ProjectView): RegistrationDraft {
  return {
    id: view.project.id, revision: view.revision, name: view.project.name, path: view.project.path,
    preferences: view.project.preferences ? { previewUrl: view.project.preferences.previewUrl ?? '', editor: structuredClone(view.project.preferences.editor) } : undefined,
    commands: (view.project.commands ?? []).map(command => ({ id: command.id, configuration: { ...structuredClone(command.configuration), arguments: [...(command.configuration.arguments ?? [])] } })),
  }
}

// Only invocation fields are replaced. Configuration approval is intentionally
// absent from every draft; the existing review/save path grants fresh approval.
export function candidateDraft(view: ProjectView, candidate: Candidate, target: string): RegistrationDraft | null {
  const draft = projectDraft(view)
  const commands = draft.commands ?? []
  const invocation = { executable: candidate.program, arguments: [...(candidate.arguments ?? [])], workingDirectory: candidate.workingDirectory }
  if (target === 'append') {
    if (commands.length >= 16) return null
    draft.commands = [...commands, { id: '', configuration: { name: candidate.label, ...invocation } }]
  } else {
    const index = commands.findIndex(command => command.id === target)
    if (index < 0) return null
    commands[index].configuration = { ...commands[index].configuration, ...invocation }
  }
  return draft
}
