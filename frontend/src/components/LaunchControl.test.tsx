import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { LaunchControl, LaunchReview } from './LaunchControl'
import { execute, type Plan } from '../services/lifecycle'

vi.mock('../services/lifecycle', () => ({ execute: vi.fn() }))
const mocked = vi.mocked(execute)
const plan = { token: 'review-token', kind: 'review_project', name: 'Fixture', tasks: [{ key: 'command', name: 'Server', kind: 'command', action: { executable: '/tmp/owned helper', arguments: ['two words', '; literal'], workingDirectory: '/tmp/project' }, command: { configuration: { runtime: undefined } }, delaySeconds: 0 }], warnings: [] } as unknown as Plan
beforeEach(() => mocked.mockReset())

describe('reviewed project execution', () => {
  it('shows exact argv before an explicit commit', async () => {
    mocked.mockResolvedValueOnce({ plan } as never).mockResolvedValueOnce({ message: 'Accepted' } as never)
    render(<LaunchControl kind="project" id="project" name="Fixture" />)
    expect(mocked).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Run & Preview Fixture' }))
    expect(await screen.findByText('/tmp/owned helper')).toBeVisible()
    expect(screen.getByText('["two words","; literal"]')).toBeVisible()
    expect(mocked).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: 'Launch reviewed tasks' }))
    await screen.findByText('Accepted')
    expect(mocked.mock.calls[1][0]).toMatchObject({ action: 'commit', token: 'review-token', switchPolicy: 'keep' })
  })
  it('cancels without dispatching', async () => {
    mocked.mockResolvedValue({ plan } as never)
    render(<LaunchControl kind="workspace" id="workspace" name="Coding" />)
    fireEvent.click(screen.getByRole('button', { name: 'Launch Coding' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
    expect(mocked).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
  it('queries a lost response without repeating commit', async () => {
    mocked.mockRejectedValueOnce(new Error('lost')).mockResolvedValueOnce({ message: 'Already accepted' } as never)
    render(<LaunchReview plan={plan} onClose={vi.fn()} onAccepted={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: 'Launch reviewed tasks' }))
    fireEvent.click(await screen.findByRole('button', { name: 'Check outcome' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledTimes(2))
    expect(mocked.mock.calls.map(([r]) => r.action)).toEqual(['commit', 'outcome'])
  })
  it('shows skipped unapproved tasks and changed configuration failures', async () => {
    mocked.mockResolvedValue({ code: 'failed', message: 'Configuration changed after review.' } as never)
    render(<LaunchReview plan={{ ...plan, tasks: [{ ...plan.tasks![0], blocked: 'Startup command is unapproved.' }] } as Plan} onClose={vi.fn()} onAccepted={vi.fn()} />)
    expect(screen.getByText('Skipped: Startup command is unapproved.')).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Launch reviewed tasks' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Configuration changed')
  })
  it('requires a final shutdown choice and supports cancel without side effects', () => {
    const close = vi.fn()
    render(<LaunchReview plan={{ ...plan, kind: 'review_shutdown', tasks: [] } as Plan} onClose={close} onAccepted={vi.fn()} />)
    expect(screen.getByRole('button', { name: 'Confirm Save & Shutdown' })).toBeVisible()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(close).toHaveBeenCalledOnce()
    expect(mocked).not.toHaveBeenCalled()
  })
  it('only sends shutdown without saving after explicit confirmation', async () => {
    mocked.mockResolvedValue({ message: 'Accepted' } as never)
    render(<LaunchReview plan={{ ...plan, kind: 'review_shutdown', tasks: [] } as Plan} onClose={vi.fn()} onAccepted={vi.fn()} />)
    fireEvent.click(screen.getByRole('radio', { name: 'Shutdown without saving' }))
    expect(mocked).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Confirm Shutdown without saving' }))
    await waitFor(() => expect(mocked).toHaveBeenCalledWith(expect.objectContaining({ action: 'commit', save: false })))
  })
})

it('explains an empty restore and does not allow a misleading successful launch', () => {
  render(<LaunchReview plan={{ ...plan, kind: 'review_session', tasks: [] } as Plan} onClose={vi.fn()} onAccepted={vi.fn()} />)
  expect(screen.getByRole('status')).toHaveTextContent('Nothing to launch or restore')
  expect(screen.getByRole('button', { name: 'Launch reviewed tasks' })).toBeDisabled()
  expect(mocked).not.toHaveBeenCalled()
})
