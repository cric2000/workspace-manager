import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ProjectSource } from '../../bindings/workspace-manager/internal/domain/models'
import { ProjectRescan } from './ProjectRescan'
import { ProjectRegistry } from './ProjectRegistry'
import { commitRegistration, listProjects, rescanProject, reviewRegistration, type ProjectComparison, type ProjectView, type RescanResult } from '../services/projects'

vi.mock('../services/projects', () => ({ rescanProject: vi.fn(), listProjects: vi.fn(), reviewRegistration: vi.fn(), commitRegistration: vi.fn() }))
const stamp = '2026-11-01T10:00:00Z'
const view: ProjectView = { revision: 'a'.repeat(64), project: {
  id: '1'.repeat(32), name: 'API', path: '/code/api', source: ProjectSource.SourceImported, createdAt: stamp, updatedAt: stamp,
  preferences: { previewUrl: 'http://localhost:8123/docs', editor: { executable: '/usr/bin/code', arguments: ['/code/api', ''], workingDirectory: '/code/api' }, approval: { fingerprint: 'old', approvedAt: stamp } },
  commands: [{ id: '2'.repeat(32), configuration: { name: 'My API', executable: '/usr/bin/go', arguments: ['run', './cmd/old'], workingDirectory: '/code/api', runtime: { environmentFiles: ['.env'], delaySeconds: 3, readiness: { kind: 'tcp', host: '::1', port: 8123, timeoutSeconds: 30, intervalSeconds: 1 }, stop: { signal: 'SIGINT', graceSeconds: 10 } } }, approval: { fingerprint: 'old', approvedAt: stamp } }, { id: '3'.repeat(32), configuration: { name: 'Worker', executable: '/usr/bin/go', arguments: ['run', './cmd/worker'], workingDirectory: '/code/api' } }],
} }
const comparison: ProjectComparison = { saved: view, scannedAt: stamp, report: { path: view.project.path, stacks: [], partial: false, findings: [], tools: [], candidates: [
  { label: 'Go: ./cmd/new', program: '/usr/bin/go', arguments: ['run', './cmd/new', ''], workingDirectory: '/code/api', reason: 'A new main package was parsed.', confidence: 'source' },
  { label: 'Worker candidate', program: 'go', arguments: ['run', './cmd/worker'], workingDirectory: '/code/api', reason: 'Worker main.', confidence: 'source' },
  { label: 'Missing tool', program: 'missingtool', arguments: ['dev'], workingDirectory: '/code/api', reason: 'Declared script.', confidence: 'metadata' },
] }, candidates: [{ available: true, resolvedExecutable: '/usr/bin/go', matchingCommandIds: [] }, { available: true, resolvedExecutable: '/usr/bin/go', matchingCommandIds: [view.project.commands![1].id] }, { available: false, resolvedExecutable: '', matchingCommandIds: [] }] }
const ready = (value = comparison): RescanResult => ({ comparison: structuredClone(value), code: '', message: '' })

describe('registered project rescan comparison', () => {
  beforeEach(() => {
    vi.mocked(rescanProject).mockReset().mockResolvedValue(ready())
    vi.mocked(listProjects).mockReset().mockResolvedValue({ state: { projects: [structuredClone(view)] }, committed: false, code: '', message: '' })
    vi.mocked(reviewRegistration).mockReset().mockResolvedValue({ review: { token: 'fresh-review', editing: true, expiresAt: stamp, project: structuredClone(view.project) }, code: '', message: '' })
    vi.mocked(commitRegistration).mockReset().mockResolvedValue({ state: { projects: [structuredClone(view)] }, committed: true, code: '', message: '' })
  })

  it('requires explicit candidate/target selection and shows field changes without saving or inferring removals', async () => {
    const user = userEvent.setup(), use = vi.fn()
    render(<ProjectRescan view={view} onClose={vi.fn()} onUse={use} />)
    await screen.findByRole('heading', { name: 'Saved commands and current suggestions' })
    expect(rescanProject).toHaveBeenCalledWith({ id: view.project.id, revision: view.revision }, expect.any(AbortSignal))
    expect(screen.getByLabelText('Saved command matches')).toHaveTextContent('My API: No equivalent invocation established')
    expect(screen.getByLabelText('Saved command matches')).toHaveTextContent('Worker: Equivalent invocation detected')
    expect(screen.getByRole('button', { name: 'Use candidate in draft' })).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('Detected candidate'), '0')
    expect(screen.getByRole('button', { name: 'Use candidate in draft' })).toBeDisabled()
    await user.selectOptions(screen.getByLabelText('Apply candidate to'), view.project.commands![0].id)
    const table = screen.getByRole('table', { name: 'Invocation changes for My API' })
    expect(table).toHaveTextContent('"./cmd/old"')
    expect(table).toHaveTextContent('"./cmd/new"')
    expect(table).toHaveTextContent('Literal arguments (changed)')
    expect(table).toHaveTextContent('""')
    expect(screen.getByText(/Command name, order, environment references/)).toBeInTheDocument()
    expect(commitRegistration).not.toHaveBeenCalled()
    expect(reviewRegistration).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Use candidate in draft' }))
    const draft = use.mock.calls[0][0]
    expect(draft.revision).toBe(view.revision)
    expect(draft.commands[0]).toEqual({ id: view.project.commands![0].id, configuration: { ...view.project.commands![0].configuration, arguments: ['run', './cmd/new', ''] } })
    expect(draft.commands[1].configuration).toEqual(view.project.commands![1].configuration)
    expect(draft.preferences.previewUrl).toBe(view.project.preferences!.previewUrl)
    expect(JSON.stringify(draft)).not.toContain('approval')
    draft.commands[0].configuration.runtime.environmentFiles[0] = 'changed.env'
    draft.preferences.editor.arguments[0] = 'changed'
    expect(view.project.commands![0].configuration.runtime!.environmentFiles).toEqual(['.env'])
    expect(view.project.preferences!.editor!.arguments).toEqual(['/code/api', ''])
  })

  it('adds a new command explicitly and preserves existing commands/settings without merging unavailable tools automatically', async () => {
    const user = userEvent.setup(), use = vi.fn()
    render(<ProjectRescan view={view} onClose={vi.fn()} onUse={use} />)
    await screen.findByLabelText('Detected candidate')
    await user.selectOptions(screen.getByLabelText('Detected candidate'), '2')
    expect(screen.getByText(/Executable is unavailable/)).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('Apply candidate to'), 'append')
    expect(screen.getByText(/Adds one command after the existing/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Use candidate in draft' }))
    const commands = use.mock.calls[0][0].commands
    expect(commands).toHaveLength(3)
    expect(commands[2]).toEqual({ id: '', configuration: { name: 'Missing tool', executable: 'missingtool', arguments: ['dev'], workingDirectory: '/code/api' } })
    expect(commands[0].configuration).toEqual(view.project.commands![0].configuration)
  })

  it('handles partial/empty scans honestly and respects the command-count limit', async () => {
    const user = userEvent.setup()
    const many = structuredClone(comparison)
    many.saved.project.commands = Array.from({ length: 16 }, (_, i) => ({ ...structuredClone(view.project.commands![0]), id: String(i).padStart(32, '0') }))
    vi.mocked(rescanProject).mockResolvedValueOnce(ready(many)).mockResolvedValueOnce(ready({ ...comparison, candidates: [], report: { ...comparison.report, partial: true, candidates: [], findings: [{ code: 'limit', file: 'go.mod', message: 'Inspect a smaller directory.' }] } }))
    render(<ProjectRescan view={view} onClose={vi.fn()} onUse={vi.fn()} />)
    await screen.findByLabelText('Detected candidate')
    await user.selectOptions(screen.getByLabelText('Detected candidate'), '0')
    expect(screen.getByRole('option', { name: /Add a new startup command/ })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Rescan comparison' }))
    expect(await screen.findByText(/This scan is partial/)).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Rescan findings' })).toHaveTextContent('Inspect a smaller directory')
    expect(screen.getByText(/No startup candidates were established/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Use candidate in draft' })).toBeDisabled()
    expect(commitRegistration).not.toHaveBeenCalled()
  })

  it('cancels cooperatively, ignores late results and aborts on unmount', async () => {
    let complete!: (value: RescanResult) => void
    vi.mocked(rescanProject).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const user = userEvent.setup(), component = render(<ProjectRescan view={view} onClose={vi.fn()} onUse={vi.fn()} />)
    const signal = vi.mocked(rescanProject).mock.calls[0][1]
    await user.click(screen.getByRole('button', { name: 'Cancel rescan' }))
    expect(signal.aborted).toBe(true)
    await act(async () => complete(ready()))
    expect(screen.queryByLabelText('Detected candidate')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Rescan cancelled')
    vi.mocked(rescanProject).mockReturnValueOnce(new Promise(() => {}))
    await user.click(screen.getByRole('button', { name: 'Rescan comparison' }))
    const retrySignal = vi.mocked(rescanProject).mock.calls[1][1]
    component.unmount()
    expect(retrySignal.aborted).toBe(true)
  })

  it('discards failed comparisons and handles safe conflicts and transport errors with retry', async () => {
    const user = userEvent.setup()
    vi.mocked(rescanProject).mockResolvedValueOnce({ comparison: null, code: 'conflict', message: 'Project changed. Close and reload registered projects.' }).mockRejectedValueOnce(new Error('secret=value /private/path'))
    render(<ProjectRescan view={view} onClose={vi.fn()} onUse={vi.fn()} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Close and reload registered projects')
    expect(screen.getByRole('button', { name: 'Use candidate in draft' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Rescan comparison' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByText(/secret=value/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Rescan comparison' }))
    await screen.findByLabelText('Detected candidate')
  })

  it('opens an independent project draft and still requires review and token-only approval before saving', async () => {
    const user = userEvent.setup()
    vi.mocked(reviewRegistration).mockImplementation(async draft => ({ review: { token: 'fresh-review', editing: true, expiresAt: stamp, project: { ...structuredClone(view.project), preferences: draft.preferences, commands: draft.commands } }, code: '', message: '' }))
    render(<ProjectRegistry report={null} candidate={null} />)
    await user.click(await screen.findByRole('button', { name: 'Rescan project API' }))
    await screen.findByLabelText('Detected candidate')
    await user.selectOptions(screen.getByLabelText('Detected candidate'), '0')
    await user.selectOptions(screen.getByLabelText('Apply candidate to'), view.project.commands![0].id)
    await user.click(screen.getByRole('button', { name: 'Use candidate in draft' }))
    expect(screen.getByRole('dialog', { name: 'Edit project configuration' })).toBeInTheDocument()
    expect(within(screen.getByRole('group', { name: 'Startup command 1' })).getByLabelText('Name')).toHaveValue('My API')
    expect(screen.getByLabelText('Command 1 argument 2')).toHaveValue('./cmd/new')
    expect(screen.getByLabelText('Command 1 argument 3')).toHaveValue('')
    expect(screen.getByLabelText(/Preview URL/)).toHaveValue('http://localhost:8123/docs')
    expect(commitRegistration).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Review configuration' }))
    expect(reviewRegistration).toHaveBeenCalledWith(expect.objectContaining({ id: view.project.id, revision: view.revision, commands: expect.arrayContaining([expect.objectContaining({ id: view.project.commands![0].id, configuration: expect.objectContaining({ runtime: view.project.commands![0].configuration.runtime, arguments: ['run', './cmd/new', ''] }) })]) }))
    expect(screen.getByRole('dialog', { name: 'Review project registration' })).toHaveTextContent('"./cmd/new"')
    await user.click(await screen.findByRole('button', { name: 'Approve and save project' }))
    expect(commitRegistration).toHaveBeenCalledExactlyOnceWith('fresh-review')
  })
})
