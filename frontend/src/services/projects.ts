import type { Result } from '../../bindings/workspace-manager/internal/projects/models'
export type { Candidate, Report, Stack } from '../../bindings/workspace-manager/internal/projects/detection/models'
export type { Result }
export type { CommandDraft, RegistrationDraft, RegistrationReview, ProjectView, RegistryResult, RegistryState } from '../../bindings/workspace-manager/internal/projects/models'
export type { ProjectComparison, RescanResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { CreationDraft, CreationReview, CreationResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { CreationHistoryStatus, CreationLogsResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { CreationLog } from '../../bindings/workspace-manager/internal/domain/models'
export type { RecoveryTarget, BootstrapRecoveryReport, RecoveryRegistrationReview } from '../../bindings/workspace-manager/internal/projects/models'
export const checkBootstrapRecovery = async (target: import('../../bindings/workspace-manager/internal/projects/models').RecoveryTarget, signal: AbortSignal) => {
  const { CheckBootstrapRecovery } = await bridge()
  if (signal.aborted) throw new DOMException('Recovery check cancelled', 'AbortError')
  return CheckBootstrapRecovery(target).cancelOn(signal)
}
export const reviewRecoveryRegistration = async (target: import('../../bindings/workspace-manager/internal/projects/models').RecoveryTarget) => (await bridge()).ReviewRecoveryRegistration(target)
export const commitRecoveryRegistration = async (token: string, signal: AbortSignal) => {
  const { CommitRecoveryRegistration } = await bridge()
  if (signal.aborted) throw new DOMException('Restore cancelled', 'AbortError')
  return CommitRecoveryRegistration(token).cancelOn(signal)
}
export const getRecoveryRegistrationOutcome = async (token: string) => (await bridge()).GetRecoveryRegistrationOutcome(token)
export const listCreationLogs = async (before: string, signal: AbortSignal) => {
  const { ListCreationLogs } = await bridge()
  if (signal.aborted) throw new DOMException('History loading cancelled', 'AbortError')
  return ListCreationLogs(before).cancelOn(signal)
}
export type { Definition as TemplateDefinition } from '../../bindings/workspace-manager/internal/projects/bootstrap/models'

const bridge = () => import('../../bindings/workspace-manager/internal/projects/service')
export const listProjects = async () => (await bridge()).ListProjects()
export const listTemplates = async () => (await bridge()).ListTemplates()
export const reviewCreation = async (draft: import('../../bindings/workspace-manager/internal/projects/models').CreationDraft) => (await bridge()).ReviewCreation(draft)
export const getCreationOutcome = async (token: string) => (await bridge()).GetCreationOutcome(token)
export const commitCreation = async (token: string, signal: AbortSignal) => {
  const { CommitCreation } = await bridge()
  if (signal.aborted) throw new DOMException('Creation cancelled', 'AbortError')
  return CommitCreation(token).cancelOn(signal)
}
export const removeProject = async (target: import('../../bindings/workspace-manager/internal/projects/models').ProjectTarget) => (await bridge()).RemoveProject(target)
export const browseProjectDirectory = async () => (await bridge()).BrowseDirectory()
export const reviewRegistration = async (draft: import('../../bindings/workspace-manager/internal/projects/models').RegistrationDraft) => (await bridge()).ReviewRegistration(draft)
export const commitRegistration = async (token: string) => (await bridge()).CommitRegistration(token)

export async function rescanProject(target: import('../../bindings/workspace-manager/internal/projects/models').ProjectTarget, signal: AbortSignal) {
  const { RescanProject } = await bridge()
  if (signal.aborted) throw new DOMException('Rescan cancelled', 'AbortError')
  return RescanProject(target).cancelOn(signal)
}

export async function inspectProject(path: string, signal: AbortSignal): Promise<Result> {
  const { Inspect } = await import('../../bindings/workspace-manager/internal/projects/service')
  if (signal.aborted) throw new DOMException('Inspection cancelled', 'AbortError')
  return Inspect(path).cancelOn(signal)
}

export const getPreparationOutcome = async (token: string) => (await bridge()).GetPreparationOutcome(token)
export const prepareCreation = async (token: string, signal: AbortSignal) => {
  const { PrepareCreation } = await bridge()
  if (signal.aborted) throw new DOMException('Preparation cancelled', 'AbortError')
  return PrepareCreation(token).cancelOn(signal)
}

export type { SetupReview, SetupResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { ToolVersionCommand, ToolVersionResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { SetupLogsResult } from '../../bindings/workspace-manager/internal/projects/models'
export type { SetupLog } from '../../bindings/workspace-manager/internal/domain/models'
export const listSetupLogs = async (projectId: string, signal: AbortSignal) => {
  const { ListSetupLogs } = await bridge()
  if (signal.aborted) throw new DOMException('History loading cancelled', 'AbortError')
  return ListSetupLogs(projectId).cancelOn(signal)
}
export const checkToolVersions = async (scope: 'creation' | 'setup', token: string, signal: AbortSignal) => {
  const service = await bridge()
  if (signal.aborted) throw new DOMException('Version checks cancelled', 'AbortError')
  return (scope === 'creation' ? service.CheckCreationToolVersions(token) : service.CheckSetupToolVersions(token)).cancelOn(signal)
}
export const reviewSetup = async (creationToken: string, kind: string) => (await bridge()).ReviewSetup(creationToken, kind)
export const getSetupOutcome = async (token: string) => (await bridge()).GetSetupOutcome(token)
export const commitSetup = async (token: string, signal: AbortSignal) => {
  const { CommitSetup } = await bridge()
  if (signal.aborted) throw new DOMException('Setup cancelled', 'AbortError')
  return CommitSetup(token).cancelOn(signal)
}
