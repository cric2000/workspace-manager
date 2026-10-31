import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { WorkspaceApplications } from './WorkspaceApplications'
import { SavedCustomApplications } from './SavedCustomApplications'
import { listWorkspaces, type View } from '../services/workspaces'
import { launchApplication, listApplications, reviewCustomApplication } from '../services/applications'
import { DuplicatePolicy } from '../../bindings/workspace-manager/internal/domain/models'

vi.mock('../services/workspaces', () => ({ listWorkspaces: vi.fn() }))
vi.mock('../services/applications', () => ({ listApplications: vi.fn(), getApplicationIcon: vi.fn(), reviewApplication: vi.fn(), reviewCustomApplication: vi.fn(), launchApplication: vi.fn() }))
const custom = { id: 'c'.repeat(32), name: 'My tool', executable: '/opt/My Tool/editor', workingDirectory: '/home/example/project', arguments: ['two words', ''], instanceBusName: '' }
const workspace: View = { id: 'a'.repeat(32), name: 'Coding', description: '', projectCount: 0, applications: [{ desktopId: '', custom, duplicatePolicy: DuplicatePolicy.SkipIfRunning }], createdAt: '', updatedAt: '', revision: 'b'.repeat(64) }
const target = { workspaceId: workspace.id, revision: workspace.revision, customId: custom.id }

describe('custom application configuration and launch', () => {
  beforeEach(() => {
    vi.mocked(listApplications).mockReset().mockResolvedValue({ entries: [], code: '', message: '' })
    vi.mocked(listWorkspaces).mockReset().mockResolvedValue({ code: '', message: '', committed: false, state: { workspaces: [workspace], defaultWorkspaceId: '', defaultMissing: false, settingsRevision: '' } })
    vi.mocked(reviewCustomApplication).mockReset().mockResolvedValue({ token: 'review-token', entry: null, custom, code: '', message: '', policy: DuplicatePolicy.SkipIfRunning, instance: { state: 'unknown', message: 'Running state is unknown.' } })
    vi.mocked(launchApplication).mockReset().mockResolvedValue({ code: 'instance_unknown', message: 'No launch was requested because running state could not be confirmed.' })
  })

  it('adds a custom tool to a draft with literal and empty arguments, without executing', async () => {
    const user = userEvent.setup(), save = vi.fn()
    render(<WorkspaceApplications workspace={{ ...workspace, applications: [] }} pending={false} blocked={false} error={null} onClose={vi.fn()} onSave={save} />)
    await user.click(screen.getByRole('button', { name: 'Add custom executable' }))
    expect(screen.getByRole('button', { name: 'Save applications' })).toBeDisabled()
    await user.type(screen.getByLabelText('Name'), 'Fixture tool')
    await user.type(screen.getByLabelText('Executable path'), '/opt/My Tool/editor')
    await user.type(screen.getByLabelText('Working directory'), '/home/example/project')
    await user.click(screen.getByRole('button', { name: 'Add argument' }))
    await user.type(screen.getByLabelText('Argument 1'), 'two words; $HOME %U')
    await user.click(screen.getByRole('button', { name: 'Add argument' }))
    await user.click(screen.getByRole('button', { name: 'Keep in draft' }))
    expect(save).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Save applications' }))
    expect(save).toHaveBeenCalledWith([{ desktopId: '', duplicatePolicy: DuplicatePolicy.SkipIfRunning, custom: { id: '', name: 'Fixture tool', executable: '/opt/My Tool/editor', arguments: ['two words; $HOME %U', ''], workingDirectory: '/home/example/project', instanceBusName: '' } }])
    expect(launchApplication).not.toHaveBeenCalled()
  })

  it('edits an independent custom draft and supports discard and removal', async () => {
    const user = userEvent.setup(), save = vi.fn()
    render(<WorkspaceApplications workspace={workspace} pending={false} blocked={false} error={null} onClose={vi.fn()} onSave={save} />)
    await user.click(screen.getByRole('button', { name: 'Edit custom application My tool' }))
    await user.clear(screen.getByLabelText('Argument 1')); await user.type(screen.getByLabelText('Argument 1'), 'changed')
    await user.click(screen.getByRole('button', { name: 'Keep in draft' }))
    expect(custom.arguments).toEqual(['two words', ''])
    await user.click(screen.getByRole('button', { name: 'Edit custom application My tool' }))
    await user.clear(screen.getByLabelText('Name')); await user.type(screen.getByLabelText('Name'), 'Discarded')
    await user.click(screen.getByRole('button', { name: 'Discard tool changes' }))
    expect(screen.queryByText('Discarded')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Remove My tool' }))
    await user.click(screen.getByRole('button', { name: 'Save applications' }))
    expect(save).toHaveBeenCalledWith([])
  })

  it('reviews a saved revision and requires an explicit fresh review to launch with unknown identity', async () => {
    const user = userEvent.setup()
    render(<SavedCustomApplications />)
    const tool = await screen.findByRole('article', { name: 'Custom tool My tool in Coding' })
    expect(within(tool).getByRole('combobox')).toHaveValue(DuplicatePolicy.SkipIfRunning)
    await user.click(within(tool).getByRole('button', { name: 'Review launch' }))
    await screen.findByRole('button', { name: 'Confirm launch' })
    expect(reviewCustomApplication).toHaveBeenCalledWith(target, DuplicatePolicy.SkipIfRunning)
    expect(screen.getByRole('dialog')).toHaveTextContent('"two words"')
    expect(screen.getByRole('dialog')).toHaveTextContent('""')
    await user.click(screen.getByRole('button', { name: 'Confirm launch' }))
    expect(await screen.findByRole('status')).toHaveTextContent('No launch was requested')
    await user.selectOptions(screen.getByLabelText('Review duplicate preference'), DuplicatePolicy.AlwaysLaunch)
    vi.mocked(reviewCustomApplication).mockResolvedValueOnce({ token: 'explicit-token', entry: null, custom, code: '', message: '', policy: DuplicatePolicy.AlwaysLaunch, instance: { state: 'unknown', message: 'Running state is unknown.' } })
    await user.click(screen.getByRole('button', { name: 'Review again' }))
    expect(reviewCustomApplication).toHaveBeenLastCalledWith(target, DuplicatePolicy.AlwaysLaunch)
    vi.mocked(launchApplication).mockResolvedValueOnce({ code: 'requested', message: 'Launch request accepted.' })
    await user.click(await screen.findByRole('button', { name: 'Confirm launch' }))
    expect(launchApplication).toHaveBeenLastCalledWith('explicit-token')
  })

  it('refreshes saved tools and hides raw connection errors', async () => {
    const user = userEvent.setup()
    vi.mocked(listWorkspaces).mockRejectedValueOnce(new Error('private=token'))
    render(<SavedCustomApplications />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByText('private=token')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Refresh saved tools' }))
    await screen.findByRole('article', { name: 'Custom tool My tool in Coding' })
    vi.mocked(listWorkspaces).mockResolvedValueOnce({ code: '', message: '', committed: false, state: { workspaces: [], defaultMissing: false, defaultWorkspaceId: '', settingsRevision: '' } })
    await user.click(screen.getByRole('button', { name: 'Refresh saved tools' }))
    expect(await screen.findByText('No saved custom applications.')).toBeInTheDocument()
  })
})
