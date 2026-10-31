import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { DuplicatePolicy } from '../../bindings/workspace-manager/internal/domain/models'
import { ApplicationLaunch } from './ApplicationLaunch'
import { reviewApplication, launchApplication, type ReviewResult, type LaunchResult } from '../services/applications'

vi.mock('../services/applications', () => ({ reviewApplication: vi.fn(), reviewCustomApplication: vi.fn(), launchApplication: vi.fn() }))
const reviewed: ReviewResult = {
  policy: DuplicatePolicy.AlwaysLaunch, instance: { state: 'unknown', message: 'Running state is unknown.' }, token: 'opaque-review-token', code: '', message: '', entry: {
    id: 'editor.desktop', name: 'Current Editor', genericName: '', description: '', icon: '',
    desktopFile: '/usr/share/applications/editor.desktop', executable: '/usr/bin/editor',
    commandline: '/usr/bin/editor "two words" %U', workingDirectory: '/home/example/projects',
    terminal: true, dbusActivatable: true, visible: true,
  },
}
const requested: LaunchResult = { code: 'requested', message: 'Launch request accepted by the desktop. Readiness is not verified.' }
async function openReview() {
  const user = userEvent.setup()
  render(<ApplicationLaunch id="editor.desktop" />)
  await user.click(screen.getByRole('button', { name: 'Review launch' }))
  await screen.findByText('Current Editor')
  return user
}

describe('reviewed application launch', () => {
  beforeEach(() => {
    vi.mocked(reviewApplication).mockReset().mockResolvedValue(reviewed)
    vi.mocked(launchApplication).mockReset().mockResolvedValue(requested)
  })

  it('shows fresh details and the unknown-instance warning, cancelling without launch', async () => {
    const user = await openReview()
    expect(reviewApplication).toHaveBeenCalledWith('editor.desktop', DuplicatePolicy.AlwaysLaunch)
    expect(screen.getByRole('dialog')).toHaveTextContent(reviewed.entry!.commandline)
    expect(screen.getByRole('dialog')).toHaveTextContent('/home/example/projects')
    expect(screen.getByRole('dialog')).toHaveTextContent('D-Bus')
    expect(screen.getByRole('dialog')).toHaveTextContent('uses the desktop terminal configuration')
    expect(screen.getByRole('dialog')).toHaveTextContent('Running state is unknown')
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Cancel' }))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(launchApplication).not.toHaveBeenCalled()
  })

  it('confirms once, blocks repeated clicks and closing while pending, and reports a handoff', async () => {
    let complete!: (result: LaunchResult) => void
    vi.mocked(launchApplication).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const user = await openReview()
    await user.dblClick(screen.getByRole('button', { name: 'Confirm launch' }))
    expect(launchApplication).toHaveBeenCalledExactlyOnceWith(reviewed.token)
    expect(screen.getByRole('button', { name: 'Sending launch request…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    await act(async () => complete(requested))
    expect(screen.getByRole('status')).toHaveTextContent('Readiness is not verified')
    expect(screen.queryByRole('button', { name: 'Confirm launch' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('checks an interrupted response with the same token, without creating another review', async () => {
    vi.mocked(launchApplication).mockRejectedValueOnce(new Error('private=secret'))
    const user = await openReview()
    await user.click(screen.getByRole('button', { name: 'Confirm launch' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('may have opened')
    expect(screen.getByRole('dialog')).not.toHaveTextContent('private=secret')
    expect(screen.queryByRole('button', { name: 'Review again' })).not.toBeInTheDocument()
    vi.mocked(launchApplication).mockResolvedValueOnce({ code: 'in_progress', message: 'Still being processed.' })
    await user.click(screen.getByRole('button', { name: 'Check launch result' }))
    expect(screen.getByRole('status')).toHaveTextContent('Still being processed')
    await user.click(screen.getByRole('button', { name: 'Check launch result' }))
    expect(screen.getByRole('status')).toHaveTextContent('Launch request accepted')
    expect(launchApplication).toHaveBeenNthCalledWith(3, reviewed.token)
    expect(reviewApplication).toHaveBeenCalledTimes(1)
  })

  it.each(['changed', 'failed', 'unavailable', 'review_required'])('requires a fresh review after %s', async code => {
    vi.mocked(launchApplication).mockResolvedValueOnce({ code, message: 'Review the current desktop entry again.' })
    const user = await openReview()
    await user.click(screen.getByRole('button', { name: 'Confirm launch' }))
    expect(screen.getByRole('status')).toHaveTextContent('Review the current desktop entry again')
    vi.mocked(reviewApplication).mockResolvedValueOnce({ ...reviewed, token: 'new-token' })
    await user.click(screen.getByRole('button', { name: 'Review again' }))
    await user.click(await screen.findByRole('button', { name: 'Confirm launch' }))
    expect(launchApplication).toHaveBeenLastCalledWith('new-token')
  })

  it('handles review failures and ignores a review returned after cancellation', async () => {
    vi.mocked(reviewApplication).mockRejectedValueOnce(new Error('private review details'))
    const user = userEvent.setup()
    render(<ApplicationLaunch id="editor.desktop" />)
    await user.click(screen.getByRole('button', { name: 'Review launch' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByRole('button', { name: 'Confirm launch' })).not.toBeInTheDocument()
    let complete!: (value: ReviewResult) => void
    vi.mocked(reviewApplication).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    await user.click(screen.getByRole('button', { name: 'Review again' }))
    expect(screen.getByRole('status')).toHaveTextContent('Reading current launch details')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await act(async () => complete(reviewed))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(launchApplication).not.toHaveBeenCalled()
  })
})
