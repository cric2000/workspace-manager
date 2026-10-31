import type { Result } from '../../bindings/workspace-manager/internal/projects/models'
export type { Candidate, Report, Stack } from '../../bindings/workspace-manager/internal/projects/detection/models'
export type { Result }
export type { CommandDraft, RegistrationDraft, RegistrationReview, ProjectView, RegistryResult, RegistryState } from '../../bindings/workspace-manager/internal/projects/models'

const bridge = () => import('../../bindings/workspace-manager/internal/projects/service')
export const listProjects = async () => (await bridge()).ListProjects()
export const browseProjectDirectory = async () => (await bridge()).BrowseDirectory()
export const reviewRegistration = async (draft: import('../../bindings/workspace-manager/internal/projects/models').RegistrationDraft) => (await bridge()).ReviewRegistration(draft)
export const commitRegistration = async (token: string) => (await bridge()).CommitRegistration(token)

export async function inspectProject(path: string, signal: AbortSignal): Promise<Result> {
  const { Inspect } = await import('../../bindings/workspace-manager/internal/projects/service')
  if (signal.aborted) throw new DOMException('Inspection cancelled', 'AbortError')
  return Inspect(path).cancelOn(signal)
}
