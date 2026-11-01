import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { WorkspaceProjects } from './WorkspaceProjects'
import { listProjects, type ProjectView } from '../services/projects'
import { ProjectSource } from '../../bindings/workspace-manager/internal/domain/models'
import type { View } from '../services/workspaces'

vi.mock('../services/projects', () => ({ listProjects: vi.fn() }))
const project = (id: string, name: string): ProjectView => ({ revision: id.repeat(64), project: { id: id.repeat(32), name, path: `/offline/${name}`, source: ProjectSource.SourceImported, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z' } })
const api = project('a', 'API'), worker = project('b', 'Worker')
const missing = 'c'.repeat(32)
const workspace: View = { id: 'd'.repeat(32), revision: 'e'.repeat(64), name: 'Coding', description: '', projectCount: 1, projectIds: [missing], applications: [], createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z' }
beforeEach(() => { vi.mocked(listProjects).mockReset().mockResolvedValue({ state: { projects: [api, worker] }, code: '', message: '', committed: false }) })

it('retains missing references, saves explicit order and current revisions only on request', async () => {
  const user = userEvent.setup(), save = vi.fn()
  render(<WorkspaceProjects workspace={workspace} pending={false} blocked={false} error={null} onClose={vi.fn()} onSave={save} />)
  await user.click(await screen.findByRole('button', { name: 'Add API to workspace' }))
  await user.click(screen.getByRole('button', { name: 'Add Worker to workspace' }))
  await user.click(screen.getByRole('button', { name: 'Move Worker up' }))
  expect(save).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save workspace projects' }))
  expect(save).toHaveBeenLastCalledWith([{ id: missing, revision: '' }, { id: worker.project.id, revision: worker.revision }, { id: api.project.id, revision: api.revision }])
  await user.click(screen.getByRole('button', { name: `Remove Unavailable project ${missing} from workspace` }))
  await user.click(screen.getByRole('button', { name: 'Save workspace projects' }))
  expect(save).toHaveBeenLastCalledWith([{ id: worker.project.id, revision: worker.revision }, { id: api.project.id, revision: api.revision }])
})

it('does not treat a failed catalogue as empty and preserves selection through refresh', async () => {
  const user = userEvent.setup(), save = vi.fn()
  vi.mocked(listProjects).mockRejectedValueOnce(new Error('private detail'))
  render(<WorkspaceProjects workspace={workspace} pending={false} blocked={false} error={null} onClose={vi.fn()} onSave={save} />)
  expect(await screen.findByRole('alert')).not.toHaveTextContent('private detail')
  expect(screen.getByRole('button', { name: 'Save workspace projects' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Reload project list' }))
  await screen.findByRole('button', { name: 'Add API to workspace' })
  await user.click(screen.getByRole('button', { name: 'Save workspace projects' }))
  expect(save).toHaveBeenCalledWith([{ id: missing, revision: '' }])
})

it('preserves the draft but blocks edits and submission after a parent conflict', async () => {
  render(<WorkspaceProjects workspace={workspace} pending={false} blocked error={<p role="alert">Changed elsewhere</p>} onClose={vi.fn()} onSave={vi.fn()} />)
  expect(await screen.findByRole('button', { name: 'Add API to workspace' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save workspace projects' })).toBeDisabled()
  expect(screen.getByRole('button', { name: `Remove Unavailable project ${missing} from workspace` })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
})
