import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { ProjectRemoval } from './ProjectRemoval'
import { removeProject, type ProjectView, type RegistryResult } from '../services/projects'
import { ProjectSource } from '../../bindings/workspace-manager/internal/domain/models'

vi.mock('../services/projects', () => ({ removeProject: vi.fn() }))
const view: ProjectView = { revision: 'a'.repeat(64), project: { id: 'b'.repeat(32), name: 'API', path: '/code/api', source: ProjectSource.SourceImported, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z' } }
beforeEach(() => { vi.mocked(removeProject).mockReset() })

it('blocks dismissal and duplicate requests until removal resolves', async () => {
  const user = userEvent.setup(), removed = vi.fn(), close = vi.fn()
  let resolve!: (result: RegistryResult) => void
  vi.mocked(removeProject).mockReturnValue(new Promise(done => { resolve = done }))
  render(<ProjectRemoval view={view} onClose={close} onRemoved={removed} />)
  await user.dblClick(screen.getByRole('button', { name: 'Remove registration' }))
  expect(removeProject).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
  const result = { state: { projects: [] }, committed: true, code: '', message: '' }
  await act(async () => resolve(result))
  expect(removed).toHaveBeenCalledWith(result)
})

it('shows workspace dependencies and requires reopening after blocked removal', async () => {
  const user = userEvent.setup(), removed = vi.fn()
  vi.mocked(removeProject).mockResolvedValue({ state: null, committed: false, code: 'project_in_use', message: 'Remove this project from its workspace profiles first: Coding.' })
  render(<ProjectRemoval view={view} onClose={vi.fn()} onRemoved={removed} />)
  await user.click(screen.getByRole('button', { name: 'Remove registration' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Coding')
  expect(screen.getByRole('button', { name: 'Remove registration' })).toBeDisabled()
  expect(removed).not.toHaveBeenCalled()
})

it('blocks blind retry after a lost response and keeps transport details private', async () => {
  const user = userEvent.setup()
  vi.mocked(removeProject).mockRejectedValue(new Error('private detail'))
  render(<ProjectRemoval view={view} onClose={vi.fn()} onRemoved={vi.fn()} />)
  await user.click(screen.getByRole('button', { name: 'Remove registration' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('reload registered projects')
  expect(screen.getByRole('alert')).not.toHaveTextContent('private detail')
  expect(screen.getByRole('button', { name: 'Remove registration' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
})
