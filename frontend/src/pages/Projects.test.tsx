import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Projects } from './Projects'
import { browseProjectDirectory, inspectProject, listProjects, listCreationLogs, type Report, type Result } from '../services/projects'

vi.mock('../services/projects', () => ({ listCreationLogs: vi.fn(), inspectProject: vi.fn(), listProjects: vi.fn(), browseProjectDirectory: vi.fn() }))
const report: Report = {
  path: '/canonical/project with spaces', partial: false,
  stacks: [{ language: 'Go', frameworks: [], packageManagers: ['Go modules'], evidence: ['go.mod'], entryPoints: ['cmd/api', 'cmd/worker'] }],
  candidates: [{ label: 'Go: ./cmd/api', program: 'go', arguments: ['run', './cmd/api'], workingDirectory: '/canonical/project with spaces', reason: 'A parsed main function exists.', confidence: 'source' }],
  tools: [{ name: 'go', available: true }], findings: [{ code: 'multiple_go_mains', file: 'go.mod', message: 'Choose the intended Go entry point.' }],
}
const ready = (value = report): Result => ({ report: value, code: '', message: '' })

describe('read-only project inspection', () => {
  beforeEach(() => {
    vi.mocked(inspectProject).mockReset().mockResolvedValue(ready())
    vi.mocked(listProjects).mockReset().mockResolvedValue({ state: { projects: [] }, code: '', message: '', committed: false })
    vi.mocked(browseProjectDirectory).mockReset().mockResolvedValue({ path: '/chosen', code: '', message: '' })
  })
  it('chooses a directory without scanning or registering it and preserves the path on cancel', async () => {
    const user = userEvent.setup()
    render(<Projects />)
    await user.click(screen.getByRole('button', { name: 'Browse directories' }))
    expect(screen.getByLabelText('Project directory')).toHaveValue('/chosen')
    expect(inspectProject).not.toHaveBeenCalled()
    vi.mocked(browseProjectDirectory).mockResolvedValueOnce({ path: '', code: 'cancelled', message: '' })
    await user.click(screen.getByRole('button', { name: 'Browse directories' }))
    expect(screen.getByLabelText('Project directory')).toHaveValue('/chosen')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('accepts an absolute path, shows evidence and exact arguments without approval or execution controls', async () => {
    const user = userEvent.setup()
    render(<Projects />)
    expect(inspectProject).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Inspect directory' })).toBeDisabled()
    await user.type(screen.getByLabelText('Project directory'), '/selected/link')
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    expect(inspectProject).toHaveBeenCalledWith('/selected/link', expect.any(AbortSignal))
    expect(await screen.findByRole('region', { name: 'Project inspection results' })).toHaveTextContent('/canonical/project with spaces')
    expect(screen.getByRole('article', { name: 'Go detection' })).toHaveTextContent('cmd/worker')
    expect(screen.getByRole('region', { name: 'Inspection findings' })).toHaveTextContent('Choose the intended Go entry point')
    await user.click(screen.getByRole('button', { name: 'Inspect candidate Go: ./cmd/api' }))
    expect(screen.getByRole('region', { name: 'Candidate details' })).toHaveTextContent('"./cmd/api"')
    expect(screen.getByRole('region', { name: 'Required tools' })).toHaveTextContent('Found on PATH')
    expect(screen.queryByRole('button', { name: /^(Save|Approve|Run|Launch|Import)/ })).not.toBeInTheDocument()
  })

  it('rejects relative paths locally and handles safe directory and transport failures with retry', async () => {
    const user = userEvent.setup()
    render(<Projects />)
    const input = screen.getByLabelText('Project directory')
    await user.type(input, 'relative/path'); await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    expect(screen.getByRole('alert')).toHaveTextContent('absolute project directory')
    expect(inspectProject).not.toHaveBeenCalled()
    await user.clear(input); await user.type(input, '/selected')
    vi.mocked(inspectProject).mockResolvedValueOnce({ report: null, code: 'directory', message: 'Check directory permissions.' })
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Check directory permissions')
    vi.mocked(inspectProject).mockRejectedValueOnce(new Error('private=token /private/path'))
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByText(/private=token/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    await screen.findByRole('region', { name: 'Project inspection results' })
  })

  it('blocks a duplicate request, cancels cooperatively and ignores the late result', async () => {
    let complete!: (value: Result) => void
    vi.mocked(inspectProject).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const user = userEvent.setup()
    render(<Projects />)
    await user.type(screen.getByLabelText('Project directory'), '/selected')
    await user.dblClick(screen.getByRole('button', { name: 'Inspect directory' }))
    expect(inspectProject).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText('Project directory')).toBeDisabled()
    const signal = vi.mocked(inspectProject).mock.calls[0][1]
    await user.click(screen.getByRole('button', { name: 'Cancel inspection' }))
    expect(signal.aborted).toBe(true)
    expect(screen.getByRole('status')).toHaveTextContent('Inspection cancelled')
    await act(async () => complete(ready()))
    expect(screen.queryByRole('region', { name: 'Project inspection results' })).not.toBeInTheDocument()
  })

  it('rescans changed metadata, clears the old selection and presents partial/empty results honestly', async () => {
    const user = userEvent.setup()
    render(<Projects />)
    await user.type(screen.getByLabelText('Project directory'), '/selected')
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    await user.click(await screen.findByRole('button', { name: 'Inspect candidate Go: ./cmd/api' }))
    vi.mocked(inspectProject).mockResolvedValueOnce(ready({ path: '/selected', partial: true, stacks: [], candidates: [], tools: [], findings: [{ code: 'scan_limit', file: '', message: 'Inspect a smaller directory.' }] }))
    await user.click(screen.getByRole('button', { name: 'Rescan directory' }))
    expect(await screen.findByRole('status')).toHaveTextContent('with limits')
    expect(screen.getByRole('region', { name: 'Startup candidates' })).toHaveTextContent('No startup command was established')
    expect(within(screen.getByRole('region', { name: 'Candidate details' })).queryByText('Go: ./cmd/api')).not.toBeInTheDocument()
    await user.clear(screen.getByLabelText('Project directory'))
    expect(screen.queryByRole('region', { name: 'Project inspection results' })).not.toBeInTheDocument()
  })

  it('cancels when navigating away and discards an unmounted response', async () => {
    let complete!: (value: Result) => void
    vi.mocked(inspectProject).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const user = userEvent.setup(), view = render(<Projects />)
    await user.type(screen.getByLabelText('Project directory'), '/selected')
    await user.click(screen.getByRole('button', { name: 'Inspect directory' }))
    const signal = vi.mocked(inspectProject).mock.calls[0][1]
    view.unmount()
    expect(signal.aborted).toBe(true)
    await act(async () => complete(ready()))
    expect(screen.queryByRole('region', { name: 'Project inspection results' })).not.toBeInTheDocument()
  })
})

it('opens creation history even without any registered projects', async () => {
  vi.mocked(listProjects).mockResolvedValueOnce({ state: { projects: [] }, code: '', message: '', committed: false })
  vi.mocked(listCreationLogs).mockResolvedValueOnce({ logs: [], next: '', code: '', message: '' })
  render(<Projects />)
  await userEvent.click(screen.getByRole('button', { name: 'Creation history' }))
  expect(await screen.findByRole('dialog')).toHaveTextContent('Creation history')
  expect(await screen.findByText('No saved creation attempts on this page.')).toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'New project' })).toBeDisabled()
  await userEvent.click(screen.getByRole('button', { name: 'Done' }))
  expect(screen.getByRole('button', { name: 'New project' })).toBeEnabled()
})
