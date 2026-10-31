import type { CatalogResult, CustomTarget, DesktopEntry, LaunchResult, ReviewResult } from '../../bindings/workspace-manager/internal/applications/models'
import { DuplicatePolicy } from '../../bindings/workspace-manager/internal/domain/models'

export type { DesktopEntry }
export type { CustomTarget, LaunchResult, ReviewResult }
export async function reviewApplication(id: string, policy = DuplicatePolicy.AlwaysLaunch): Promise<ReviewResult> {
  const { ReviewDesktop } = await import('../../bindings/workspace-manager/internal/applications/launchservice')
  return ReviewDesktop(id, policy)
}
export async function reviewCustomApplication(target: CustomTarget, policy: DuplicatePolicy): Promise<ReviewResult> {
  const { ReviewCustom } = await import('../../bindings/workspace-manager/internal/applications/launchservice')
  return ReviewCustom(target, policy)
}
export async function launchApplication(token: string): Promise<LaunchResult> {
  const { Launch } = await import('../../bindings/workspace-manager/internal/applications/launchservice')
  return Launch(token)
}
export async function listApplications(): Promise<CatalogResult> {
  const { List } = await import('../../bindings/workspace-manager/internal/applications/service')
  return List()
}
export async function getApplicationIcon(id: string): Promise<string> {
  const { GetIcon } = await import('../../bindings/workspace-manager/internal/applications/service')
  return (await GetIcon(id)).dataUrl
}
