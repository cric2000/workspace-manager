import type { Info } from '../../bindings/workspace-manager/internal/app/models'

export type { Info }

// Import lazily so runtime initialization failures reach the UI's retry path.
// Native communication stays in generated Wails bindings, outside components.
export async function getInfo(): Promise<Info> {
  const { GetInfo } = await import('../../bindings/workspace-manager/internal/app/infoservice')
  return GetInfo()
}
