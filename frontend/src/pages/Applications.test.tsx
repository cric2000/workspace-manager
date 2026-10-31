import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Applications } from './Applications'
import { getApplicationIcon, listApplications, type DesktopEntry } from '../services/applications'

vi.mock('../services/applications', () => ({ getApplicationIcon: vi.fn(), listApplications: vi.fn() }))
vi.mock('../components/SavedCustomApplications', () => ({ SavedCustomApplications: () => null }))
const editor: DesktopEntry = { id: 'org.example.Editor.desktop', name: 'Example Editor', genericName: 'Text Editor', description: 'Edit your projects', desktopFile: '/usr/share/applications/org.example.Editor.desktop', icon: 'editor-icon', commandline: '/usr/bin/editor "two words" %U', executable: '/usr/bin/editor', workingDirectory: '', terminal: false, dbusActivatable: false, visible: true }
const terminal: DesktopEntry = { ...editor, id: 'org.example.Terminal.desktop', name: 'Example Terminal', genericName: 'Terminal', description: 'Work with commands', commandline: '/usr/bin/terminal', executable: '/usr/bin/terminal', dbusActivatable: true, terminal: true }
const catalogue = (entries: DesktopEntry[] = [editor, terminal]) => ({ entries, code: '', message: '' })

describe('installed application browser', () => {
  beforeEach(() => {
    vi.mocked(listApplications).mockReset().mockResolvedValue(catalogue())
    vi.mocked(getApplicationIcon).mockReset().mockResolvedValue('')
  })

  it('shows pending discovery and prevents refresh while loading', () => {
    vi.mocked(listApplications).mockReturnValue(new Promise(() => {}))
    render(<Applications />)
    expect(screen.getByRole('status')).toHaveTextContent('Reading installed applications')
    expect(screen.getByRole('button', { name: 'Refresh applications' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: /Inspect/ })).not.toBeInTheDocument()
  })

  it('searches name, category and stable ID and inspects desktop metadata without launching', async () => {
    const user = userEvent.setup()
    render(<Applications />)
    expect(await screen.findByRole('status')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: `Inspect ${editor.name} (${editor.id})` }))
    const details = screen.getByRole('region', { name: 'Application details' })
    expect(details).toHaveTextContent(editor.commandline)
    expect(details).toHaveTextContent(editor.desktopFile)
    expect(details).toHaveTextContent('Installed')
    expect(details).toHaveTextContent('Running state and readiness are not verified')
    const search = screen.getByRole('searchbox')
    await user.type(search, 'TERMINAL')
    expect(screen.queryByRole('button', { name: /Inspect Example Editor/ })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Inspect Example Terminal/ }))
    expect(details).toHaveTextContent('Desktop activation (D-Bus)')
    expect(details).toHaveTextContent('Fallback launch entry')
    expect(details).toHaveTextContent('Required')
    await user.clear(search)
    await user.type(search, 'org.example.Editor')
    expect(screen.getByRole('button', { name: /Inspect Example Editor/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Inspect Example Terminal/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Launch/ })).not.toBeInTheDocument()
  })

  it('keeps equal display names independently selectable by application ID', async () => {
    const user = userEvent.setup()
    vi.mocked(listApplications).mockResolvedValue(catalogue([editor, { ...terminal, name: editor.name }]))
    render(<Applications />)
    const second = await screen.findByRole('button', { name: `Inspect ${editor.name} (${terminal.id})` })
    await user.click(second)
    expect(second).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('region', { name: 'Application details' })).toHaveTextContent(terminal.id)
  })

  it('reports discovery/transport failures safely and allows retry', async () => {
    const user = userEvent.setup()
    vi.mocked(listApplications).mockRejectedValueOnce(new Error('token=secret /private/path'))
    render(<Applications />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByText(/token=secret/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry discovery' }))
    expect(await screen.findByRole('button', { name: /Inspect Example Editor/ })).toBeInTheDocument()
    vi.mocked(listApplications).mockResolvedValueOnce({ entries: [], code: 'discovery_failed', message: 'Could not read installed applications.' })
    await user.click(screen.getByRole('button', { name: 'Refresh applications' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not read')
    expect(screen.queryByRole('button', { name: /Inspect/ })).not.toBeInTheDocument()
  })

  it('distinguishes an empty desktop catalogue from an empty search', async () => {
    const user = userEvent.setup()
    vi.mocked(listApplications).mockResolvedValueOnce(catalogue([]))
    render(<Applications />)
    expect(await screen.findByText('No applications found')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Refresh applications' }))
    await user.type(await screen.findByRole('searchbox'), 'missing-tool')
    expect(screen.getByText('No matching applications')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('paginates the catalogue and returns to the first page when searching', async () => {
    const user = userEvent.setup()
    vi.mocked(listApplications).mockResolvedValue(catalogue(Array.from({ length: 30 }, (_, index) => ({ ...editor, id: `org.example.Editor${index}.desktop`, name: `Editor ${index}` }))))
    render(<Applications />)
    await screen.findByText('Page 1 of 2')
    expect(screen.getAllByRole('button', { name: /^Inspect/ })).toHaveLength(24)
    await user.click(screen.getByRole('button', { name: 'Next application page' }))
    expect(screen.getAllByRole('button', { name: /^Inspect/ })).toHaveLength(6)
    expect(screen.getByRole('button', { name: 'Next application page' })).toBeDisabled()
    await user.type(screen.getByRole('searchbox'), 'Editor 0')
    expect(screen.getAllByRole('button', { name: /^Inspect/ })).toHaveLength(1)
    expect(screen.getByRole('button', { name: /Inspect Editor 0/ })).toBeInTheDocument()
  })

  it('shares PNG icon loads between rows/details and falls back on a broken image', async () => {
    const user = userEvent.setup()
    vi.mocked(getApplicationIcon).mockResolvedValue('data:image/png;base64,dGVzdA==')
    const { container } = render(<Applications />)
    await user.click(await screen.findByRole('button', { name: /Inspect Example Editor/ }))
    await waitFor(() => expect(container.querySelectorAll('img')).toHaveLength(3))
    expect(getApplicationIcon).toHaveBeenCalledTimes(2)
    const image = container.querySelector('img')!
    fireEvent.error(image)
    expect(container.querySelectorAll('img')).toHaveLength(2)
  })

  it('ignores non-PNG icon URLs and recovers gracefully from icon failures', async () => {
    vi.mocked(getApplicationIcon).mockResolvedValueOnce('file:///private/icon.svg').mockRejectedValueOnce(new Error('private error'))
    const { container } = render(<Applications />)
    await screen.findByRole('button', { name: /Inspect Example Editor/ })
    await waitFor(() => expect(getApplicationIcon).toHaveBeenCalledTimes(2))
    expect(container.querySelector('img')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('refreshes registrations and icons, clearing a selection which disappeared', async () => {
    const user = userEvent.setup()
    render(<Applications />)
    await user.click(await screen.findByRole('button', { name: /Inspect Example Editor/ }))
    vi.mocked(listApplications).mockResolvedValueOnce(catalogue([terminal]))
    await user.click(screen.getByRole('button', { name: 'Refresh applications' }))
    await screen.findByText('1 installed application')
    expect(screen.getByRole('region', { name: 'Application details' })).toHaveTextContent('Get to know your tools')
    expect(screen.queryByRole('button', { name: /Inspect Example Editor/ })).not.toBeInTheDocument()
    expect(getApplicationIcon).toHaveBeenCalledTimes(3)
  })

  it('ignores catalogue responses after navigation away', async () => {
    let complete!: (value: ReturnType<typeof catalogue>) => void
    vi.mocked(listApplications).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const first = render(<Applications />)
    first.unmount()
    render(<Applications />)
    await screen.findByRole('button', { name: /Inspect Example Editor/ })
    await act(async () => complete(catalogue([])))
    expect(screen.getByRole('button', { name: /Inspect Example Editor/ })).toBeInTheDocument()
  })
})
