import { StrictMode } from 'react'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { ProjectSetupHistory } from './ProjectSetupHistory'
import { listSetupLogs, type ProjectView, type SetupLog } from '../services/projects'
import { ProjectSource } from '../../bindings/workspace-manager/internal/domain/models'

vi.mock('../services/projects', () => ({ listSetupLogs: vi.fn() }))
const timestamp = '2026-11-02T01:00:00Z'
const view: ProjectView = { revision: 'a'.repeat(64), project: { id: '1'.repeat(32), name: 'API', path: '/code/api', source: ProjectSource.SourceBootstrapped, createdAt: timestamp, updatedAt: timestamp, commands: [] } }
const log: SetupLog = { id: '2'.repeat(32), projectId: view.project.id, kind: 'git', label: 'Initialize Git', recordedAt: timestamp, finishedAt: timestamp, result: { started: true, succeeded: true, exitCode: 0, output: 'Initialized empty repository.\n', truncated: false, code: '', message: 'Setup completed.' } }
beforeEach(() => vi.mocked(listSetupLogs).mockReset().mockResolvedValue({ logs: [log], code: '', message: '' }))

it('loads saved results and literal output without offering execution or retry controls', async () => {
  const close = vi.fn()
  render(<ProjectSetupHistory view={view} onClose={close} />)
  expect(await screen.findByRole('region', { name: 'Initialize Git history' })).toHaveTextContent('Succeeded. Setup completed.')
  expect(screen.getByText('Initialized empty repository.')).toBeInTheDocument()
  expect(listSetupLogs).toHaveBeenCalledExactlyOnceWith(view.project.id, expect.any(AbortSignal))
  expect(screen.queryByRole('button', { name: /run|retry|resume/i })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Done' }))
  expect(close).toHaveBeenCalledOnce()
})
it('distinguishes failed setup, spawn failure and missing final outcomes', async () => {
  vi.mocked(listSetupLogs).mockResolvedValueOnce({ logs: [
    { ...log, result: { ...log.result!, succeeded: false, exitCode: 1, truncated: true } },
    { ...log, id: '3'.repeat(32), label: 'Create virtual environment', result: { ...log.result!, started: false, succeeded: false, exitCode: -1 } },
    { ...log, id: '4'.repeat(32), label: 'Install dependencies', result: undefined, finishedAt: undefined },
  ], code: '', message: '' })
  render(<ProjectSetupHistory view={view} onClose={vi.fn()} />)
  expect(await screen.findByRole('region', { name: 'Initialize Git history' })).toHaveTextContent('partial files may remain')
  expect(screen.getByRole('region', { name: 'Initialize Git history' })).toHaveTextContent('Output limited to 64 KiB')
  expect(screen.getByRole('region', { name: 'Create virtual environment history' })).toHaveTextContent('No setup process started')
  expect(screen.getByRole('region', { name: 'Install dependencies history' })).toHaveTextContent('No final outcome was saved')
  expect(screen.getByRole('region', { name: 'Install dependencies history' })).not.toHaveTextContent('Succeeded')
})
it('shows storage errors and allows a read-only reload', async () => {
  vi.mocked(listSetupLogs).mockResolvedValueOnce({ logs: null, code: 'setup_history', message: 'Stored history could not be read. Existing logs were kept.' }).mockResolvedValueOnce({ logs: [], code: '', message: '' })
  render(<ProjectSetupHistory view={view} onClose={vi.fn()} />)
  expect(await screen.findByRole('alert')).toHaveTextContent('Existing logs were kept')
  await userEvent.click(screen.getByRole('button', { name: 'Reload setup history' }))
  expect(await screen.findByRole('status')).toHaveTextContent('No saved setup attempts')
  expect(listSetupLogs).toHaveBeenCalledTimes(2)
})
it('allows closing while loading and cancels the read on unmount', async () => {
  vi.mocked(listSetupLogs).mockReturnValueOnce(new Promise(() => {}))
  const close = vi.fn()
  const rendered = render(<ProjectSetupHistory view={view} onClose={close} />)
  expect(screen.getByRole('button', { name: 'Reload setup history' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Close dialog' }))
  expect(close).toHaveBeenCalledOnce()
  const signal = vi.mocked(listSetupLogs).mock.calls[0][1]
  rendered.unmount()
  expect(signal.aborted).toBe(true)
})
it('ignores an aborted earlier read during StrictMode remount', async () => {
  let reject!: (error: Error) => void
  vi.mocked(listSetupLogs).mockReturnValueOnce(new Promise((_, fail) => { reject = fail })).mockResolvedValueOnce({ logs: [log], code: '', message: '' })
  render(<StrictMode><ProjectSetupHistory view={view} onClose={vi.fn()} /></StrictMode>)
  await screen.findByRole('region', { name: 'Initialize Git history' })
  expect(vi.mocked(listSetupLogs).mock.calls[0][1].aborted).toBe(true)
  await act(async () => reject(new Error('aborted old read')))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'Initialize Git history' })).toBeInTheDocument()
})
