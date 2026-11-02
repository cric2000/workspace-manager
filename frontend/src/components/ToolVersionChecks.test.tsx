import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { ToolVersionChecks } from './ToolVersionChecks'
import { checkToolVersions, type ToolVersionResult } from '../services/projects'

vi.mock('../services/projects', () => ({ checkToolVersions: vi.fn() }))
const commands = [{ tool: 'node', executable: '/tools/node', arguments: ['--version'], minimumVersion: '24.12.0' }]
const result: ToolVersionResult = { checks: [{ tool: 'node', version: '24.21.0', minimumVersion: '24.12.0', status: 'supported', message: 'Meets the declared minimum.' }], code: '', message: 'Version checks finished.' }
beforeEach(() => vi.mocked(checkToolVersions).mockReset().mockResolvedValue(result))

it('shows exact version commands and runs only on an explicit request', async () => {
  const busy = vi.fn()
  render(<ToolVersionChecks scope="creation" token="review-token" commands={commands} disabled={false} onBusyChange={busy} />)
  const user = userEvent.setup()
  expect(checkToolVersions).not.toHaveBeenCalled()
  await user.click(screen.getByText('Review version commands'))
  expect(screen.getByText('/tools/node "--version"')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Check tool versions' }))
  expect(await screen.findByRole('status')).toHaveTextContent('24.21.0 (minimum 24.12.0) · supported')
  expect(checkToolVersions).toHaveBeenCalledExactlyOnceWith('creation', 'review-token', expect.any(AbortSignal))
  expect(busy).toHaveBeenCalledWith(true)
  expect(busy).toHaveBeenLastCalledWith(false)
})
it('reports unsupported and unrecognized versions without implying compatibility', async () => {
  vi.mocked(checkToolVersions).mockResolvedValueOnce({ ...result, code: 'version_attention', checks: [
    { ...result.checks![0], version: '22.0.0', status: 'unsupported', message: 'Below the required minimum.' },
    { tool: 'npm', version: '', minimumVersion: '', status: 'unknown', message: 'Unrecognized version.' },
  ] })
  render(<ToolVersionChecks scope="setup" token="setup-token" commands={commands} disabled={false} onBusyChange={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: 'Check tool versions' }))
  expect(await screen.findByRole('status')).toHaveTextContent('22.0.0 (minimum 24.12.0) · unsupported')
  expect(screen.getByRole('status')).toHaveTextContent('npm: version unconfirmed · unknown')
  expect(checkToolVersions).toHaveBeenCalledWith('setup', 'setup-token', expect.any(AbortSignal))
})
it('cancels checks, prevents duplicate requests and allows an explicit retry', async () => {
  let reject!: (error: Error) => void
  vi.mocked(checkToolVersions).mockReturnValueOnce(new Promise((_, fail) => { reject = fail }))
  render(<ToolVersionChecks scope="creation" token="review-token" commands={commands} disabled={false} onBusyChange={vi.fn()} />)
  const user = userEvent.setup()
  await user.dblClick(screen.getByRole('button', { name: 'Check tool versions' }))
  expect(checkToolVersions).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: 'Cancel version checks' }))
  expect(vi.mocked(checkToolVersions).mock.calls[0][2].aborted).toBe(true)
  await act(async () => reject(new Error('private raw detail')))
  expect(await screen.findByRole('alert')).toHaveTextContent('Version checks cancelled')
  expect(screen.getByRole('alert')).not.toHaveTextContent('private raw detail')
  expect(checkToolVersions).toHaveBeenCalledTimes(1)
  await user.click(screen.getByRole('button', { name: 'Check tool versions' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Version checks finished')
})
it('keeps failures generic and cancels the probe on unmount', async () => {
  vi.mocked(checkToolVersions).mockRejectedValueOnce(new Error('secret detail'))
  const view = render(<ToolVersionChecks scope="creation" token="review-token" commands={commands} disabled={false} onBusyChange={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: 'Check tool versions' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Version results unavailable')
  expect(screen.getByRole('alert')).not.toHaveTextContent('secret detail')
  vi.mocked(checkToolVersions).mockReturnValueOnce(new Promise(() => {}))
  await userEvent.click(screen.getByRole('button', { name: 'Check tool versions' }))
  const signal = vi.mocked(checkToolVersions).mock.calls[1][2]
  view.unmount()
  expect(signal.aborted).toBe(true)
})
it('does not run while another operation is in progress', () => {
  render(<ToolVersionChecks scope="creation" token="review-token" commands={commands} disabled onBusyChange={vi.fn()} />)
  expect(screen.getByRole('button', { name: 'Check tool versions' })).toBeDisabled()
  expect(checkToolVersions).not.toHaveBeenCalled()
})
