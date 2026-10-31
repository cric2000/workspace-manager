import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { Diagnostics } from './Diagnostics'
import { getInfo, type Info } from '../services/desktop'

vi.mock('../services/desktop', () => ({ getInfo: vi.fn() }))

const info: Info = { version: '0.1.0-test', os: 'linux', architecture: 'amd64', goVersion: 'go-test', sessionType: 'wayland' }

describe('desktop diagnostics', () => {
  beforeEach(() => {
    vi.mocked(getInfo).mockReset().mockResolvedValue(info)
    document.documentElement.removeAttribute('data-theme')
  })

  it('shows real bridge diagnostics and clearly states the current scope', async () => {
    render(<Diagnostics />)
    expect(await screen.findByText('Desktop bridge connected')).toBeInTheDocument()
    expect(screen.getByText('linux / amd64')).toBeInTheDocument()
    expect(screen.getByText('0.1.0-test')).toBeInTheDocument()
    expect(screen.getByText(/Application launching, project execution/)).toBeInTheDocument()
  })

  it('prevents repeated requests while a connection is pending', () => {
    vi.mocked(getInfo).mockReturnValue(new Promise(() => {}))
    render(<Diagnostics />)
    expect(screen.getByRole('button', { name: 'Refresh diagnostics' })).toBeDisabled()
    expect(screen.getByText('Connecting to desktop…')).toBeInTheDocument()
  })

  it('reports a bridge failure and allows recovery without exposing raw error data', async () => {
    const user = userEvent.setup()
    vi.mocked(getInfo).mockRejectedValueOnce(new Error('sensitive raw transport details'))
    render(<Diagnostics />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not connect')
    expect(screen.queryByText(/sensitive raw/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Retry connection' }))
    expect(await screen.findByText('Desktop bridge connected')).toBeInTheDocument()
    expect(getInfo).toHaveBeenCalledTimes(2)
  })

  it('ignores an old request after unmount instead of replacing a new view', async () => {
    let complete!: (value: Info) => void
    vi.mocked(getInfo).mockReturnValueOnce(new Promise(resolve => { complete = resolve }))
    const first = render(<Diagnostics />)
    first.unmount()
    render(<Diagnostics />)
    await screen.findByText('Desktop bridge connected')
    complete({ ...info, version: 'outdated-response' })
    await waitFor(() => expect(screen.queryByText('outdated-response')).not.toBeInTheDocument())
  })

})
