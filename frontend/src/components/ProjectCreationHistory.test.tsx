import { StrictMode } from 'react'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { ProjectCreationHistory } from './ProjectCreationHistory'
import { listCreationLogs, type CreationLog } from '../services/projects'

vi.mock('../services/projects', () => ({ listCreationLogs: vi.fn() }))
const timestamp = '2026-11-02T01:00:00Z'
const log: CreationLog = { id: '1'.repeat(32), projectId: '2'.repeat(32), name: 'example', path: '/code/example', templateId: 'nextjs', kind: 'preparation', recordedAt: timestamp, finishedAt: timestamp, result: { succeeded: true, created: false, registered: false, processStarted: true, exitCode: 0, fileCount: 4, output: 'Official tool output.\n', truncated: false, activity: [], code: '', message: 'Prepared files for review.' } }
beforeEach(() => vi.mocked(listCreationLogs).mockReset().mockResolvedValue({ logs: [log], next: '', code: '', message: '' }))
it('shows preparation output and distinguishes publication and registration outcomes', async () => {
  vi.mocked(listCreationLogs).mockResolvedValueOnce({ logs: [log, { ...log, id: '3'.repeat(32), kind: 'publication', result: { ...log.result!, succeeded: false, processStarted: false, created: true, registered: false, exitCode: -1, message: 'Registration failed. Files were kept.', output: '', recoveryPath: '/code/recovery', activity: ['Published reviewed files.'] } }], next: '', code: '', message: '' })
  render(<ProjectCreationHistory onClose={vi.fn()} />)
  expect(await screen.findByRole('region', { name: 'example preparation history' })).toHaveTextContent('Tool process started: yes. Exit code: 0')
  expect(screen.getByText('Official tool output.')).toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'example publication history' })).toHaveTextContent('Files published: yes. Registration saved: no')
  expect(screen.getByText('Recovery path: /code/recovery')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: /run|resume|retry/i })).not.toBeInTheDocument()
})
it('shows missing final outcomes without assuming success or live processes', async () => {
  vi.mocked(listCreationLogs).mockResolvedValueOnce({ logs: [{ ...log, result: undefined, finishedAt: undefined }], next: '', code: '', message: '' })
  render(<ProjectCreationHistory onClose={vi.fn()} />)
  const region = await screen.findByRole('region', { name: 'example preparation history' })
  expect(region).toHaveTextContent('No final outcome was saved')
  expect(region).not.toHaveTextContent('Succeeded')
})
it('pages older/newer attempts and reloads the newest page using read-only cursors', async () => {
  vi.mocked(listCreationLogs).mockResolvedValueOnce({ logs: [log], next: log.id, code: '', message: '' }).mockResolvedValueOnce({ logs: [{ ...log, name: 'older' }], next: '', code: '', message: '' })
  render(<ProjectCreationHistory onClose={vi.fn()} />)
  const user = userEvent.setup()
  await screen.findByRole('region', { name: 'example preparation history' })
  expect(screen.getByRole('button', { name: 'Newer attempts' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Older attempts' }))
  expect(await screen.findByRole('region', { name: 'older preparation history' })).toBeInTheDocument()
  expect(listCreationLogs).toHaveBeenLastCalledWith(log.id, expect.any(AbortSignal))
  await user.click(screen.getByRole('button', { name: 'Newer attempts' }))
  await screen.findByRole('region', { name: 'example preparation history' })
  expect(listCreationLogs).toHaveBeenLastCalledWith('', expect.any(AbortSignal))
  await user.click(screen.getByRole('button', { name: 'Reload newest history' }))
  await screen.findByRole('region', { name: 'example preparation history' })
  expect(listCreationLogs).toHaveBeenCalledTimes(4)
})
it('reports storage errors, then permits a read-only reload', async () => {
  vi.mocked(listCreationLogs).mockResolvedValueOnce({ logs: null, next: '', code: 'creation_history', message: 'Unreadable history. Existing records were kept.' }).mockResolvedValueOnce({ logs: [], next: '', code: '', message: '' })
  render(<ProjectCreationHistory onClose={vi.fn()} />)
  expect(await screen.findByRole('alert')).toHaveTextContent('Existing records were kept')
  await userEvent.click(screen.getByRole('button', { name: 'Reload newest history' }))
  expect(await screen.findByRole('status')).toHaveTextContent('No saved creation attempts')
})
it('cancels reads on close and ignores aborted StrictMode responses', async () => {
  let reject!: (error: Error) => void
  vi.mocked(listCreationLogs).mockReturnValueOnce(new Promise((_, fail) => { reject = fail }))
  const close = vi.fn()
  const view = render(<StrictMode><ProjectCreationHistory onClose={close} /></StrictMode>)
  await screen.findByRole('region', { name: 'example preparation history' })
  expect(vi.mocked(listCreationLogs).mock.calls[0][1].aborted).toBe(true)
  await act(async () => reject(new Error('aborted earlier response')))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  vi.mocked(listCreationLogs).mockReturnValueOnce(new Promise(() => {}))
  await userEvent.click(screen.getByRole('button', { name: 'Reload newest history' }))
  await userEvent.click(screen.getByRole('button', { name: 'Done' }))
  expect(close).toHaveBeenCalledOnce()
  const signal = vi.mocked(listCreationLogs).mock.calls[2][1]
  view.unmount()
  expect(signal.aborted).toBe(true)
})
