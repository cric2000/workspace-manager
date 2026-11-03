import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Lifecycle } from './Lifecycle'
import { execute, type Response } from '../services/lifecycle'

vi.mock('../services/lifecycle', () => ({ execute: vi.fn() }))
vi.mock('../services/workspaces', () => ({ listWorkspaces: vi.fn() }))
const mocked = vi.mocked(execute)
const base = { runtime: { busy: false, shutdownState: 'idle', tasks: [], runs: [], activeWorkspaceId: 'workspace' }, settings: { startupMode: 'fixed', appearance: 'system', autostart: false, initialDelaySeconds: 0, defaultWorkspaceId: 'workspace' }, settingsRevision: 'revision', workspaces: [{ id: 'workspace', name: 'Coding', projectIds: [], applications: [] }], projects: [], sessions: [{ value: { id: 'session', name: 'Monday', capturedAt: '2026-11-03T00:00:00Z', projects: [], applications: [], captureWarnings: ['Unsaved buffers are not captured.'] }, revision: 'snapshot-revision' }] } as unknown as Response
beforeEach(() => { mocked.mockReset(); mocked.mockImplementation(async request => request.action === 'state' ? base : { message: 'Saved' } as never) })

describe('lifecycle pages', () => {
  it('displays dashboard status without launching tasks', async () => {
    render(<Lifecycle page="dashboard" />)
    await screen.findByText('Managed workers')
    expect(screen.getByRole('button', { name: 'Launch Coding' })).toBeVisible()
    expect(mocked.mock.calls.map(([r]) => r.action)).toEqual(['state'])
  })
  it('saves a named session only on submit', async () => {
    render(<Lifecycle page="sessions" />)
    await screen.findByText('Monday')
    fireEvent.click(screen.getAllByRole('button', { name: 'Save as New Session' })[0])
    const dialog = screen.getByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Session name'), { target: { value: 'Tuesday' } })
    expect(mocked).toHaveBeenCalledTimes(1)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save session' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'capture', name: 'Tuesday' })))
  })
  it('branches independently and shows restore warnings', async () => {
    render(<Lifecycle page="sessions" />)
    await screen.findByText('Monday')
    fireEvent.click(screen.getByText('Inspect snapshot & warnings'))
    expect(screen.getByText('Unsaved buffers are not captured.')).toBeVisible()
    fireEvent.click(screen.getAllByRole('button', { name: 'Save as New Session' })[1])
    fireEvent.change(screen.getByLabelText('Session name'), { target: { value: 'Branch' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save session' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'branch', id: 'session', name: 'Branch', save: false })))
  })
  it('requires confirmation before deleting a snapshot', async () => {
    render(<Lifecycle page="sessions" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Delete…' }))
    expect(mocked).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(mocked).toHaveBeenCalledTimes(1)
  })
  it('reviews startup settings before enabling autostart', async () => {
    render(<Lifecycle page="settings" />)
    const checkbox = await screen.findByRole('checkbox', { name: 'Start the reviewed configuration after login' })
    fireEvent.click(checkbox)
    fireEvent.click(screen.getByRole('button', { name: 'Review & enable login startup' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'review_login', settings: expect.objectContaining({ autostart: true }), revision: 'revision' })))
  })
  it('shows controller failures and allows reconnect', async () => {
    mocked.mockResolvedValueOnce({ code: 'failed', message: 'Controller unavailable.' } as never)
    render(<Lifecycle page="activity" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Controller unavailable')
    fireEvent.click(screen.getByRole('button', { name: 'Refresh controller state' }))
    expect(await screen.findByText('Controller connected')).toBeVisible()
  })
  it('requires an explicit force-stop confirmation', async () => {
    mocked.mockResolvedValueOnce({ ...base, runtime: { ...base.runtime, runs: [{ id: 'run', projectId: 'project', command: { configuration: { name: 'Server', executable: '/tmp/helper', arguments: [], workingDirectory: '/tmp/project' } }, state: 'failed', startedAt: '2026-11-03T00:00:00Z', output: 'owned output' }] } } as unknown as Response)
    render(<Lifecycle page="activity" />)
    fireEvent.click(await screen.findByRole('button', { name: 'Force stop…' }))
    expect(mocked).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Confirm force stop' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'stop', id: 'run', force: true })))
  })
})

it('opens global session capture with a suggested name, and saves only after confirmation', async () => {
  const { rerender } = render(<Lifecycle page="sessions" captureRequest={1} />)
  const dialog = await screen.findByRole('dialog', { name: 'Save as New Session' })
  const name = within(dialog).getByLabelText('Session name') as HTMLInputElement
  expect(name.value).toMatch(/^Session · /)
  expect(mocked.mock.calls.map(([r]) => r.action)).toEqual(['state'])
  fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  rerender(<Lifecycle page="sessions" captureRequest={1} />)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  rerender(<Lifecycle page="sessions" captureRequest={2} />)
  fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Save session' }))
  await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'capture', name: expect.stringMatching(/^Session · /) })))
})
