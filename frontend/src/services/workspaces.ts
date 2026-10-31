import type { Draft, Result, State, Target, View } from '../../bindings/workspace-manager/internal/workspaces/models'

export type { Draft, Result, State, Target, View }
export type { WorkspaceApplication } from '../../bindings/workspace-manager/internal/domain/models'

// Native transport stays in generated bindings; lazy imports expose failures to UI.
const bridge = () => import('../../bindings/workspace-manager/internal/workspaces/service')
export const listWorkspaces = async (): Promise<Result> => (await bridge()).List()
export const createWorkspace = async (draft: Draft): Promise<Result> => (await bridge()).Create(draft)
export const editWorkspace = async (target: Target, draft: Draft): Promise<Result> => (await bridge()).Edit(target, draft)
export const duplicateWorkspace = async (target: Target, draft: Draft): Promise<Result> => (await bridge()).Duplicate(target, draft)
export const deleteWorkspace = async (target: Target): Promise<Result> => (await bridge()).Delete(target)
export const setDefaultWorkspace = async (target: Target, settingsRevision: string): Promise<Result> => (await bridge()).SetDefault(target, settingsRevision)
export const setWorkspaceApplications = async (target: Target, choices: import('../../bindings/workspace-manager/internal/domain/models').WorkspaceApplication[]): Promise<Result> => (await bridge()).SetApplications(target, choices)
