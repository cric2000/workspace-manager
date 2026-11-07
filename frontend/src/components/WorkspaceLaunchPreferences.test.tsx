import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { WorkspaceLaunchPreferences } from './WorkspaceLaunchPreferences'
import { execute } from '../services/lifecycle'

vi.mock('../services/lifecycle', () => ({ execute: vi.fn() }))
it('loads launch preferences directly and saves against the exact reviewed workspace revision', async () => {
  const onSaved = vi.fn(), onClose = vi.fn()
  vi.mocked(execute).mockResolvedValueOnce({ workspaces: [{ id: 'coding', name: 'Coding', launch: { urls: [], delaySeconds: 0 } }], workspaceRevisions: { coding: 'held-revision' } } as never).mockResolvedValueOnce({ message: 'Launch preferences saved.' } as never)
  render(<WorkspaceLaunchPreferences id="coding" name="Coding" onSaved={onSaved} onClose={onClose} />)
  const urls = await screen.findByLabelText('Browser URLs (one HTTP(S) URL per line)')
  fireEvent.change(urls, { target: { value: 'https://example.test' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save launch preferences' }))
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith('Launch preferences saved.'))
  expect(onClose).toHaveBeenCalledOnce()
  expect(execute).toHaveBeenNthCalledWith(2, expect.objectContaining({ action: 'configure_workspace', id: 'coding', revision: 'held-revision', workspaceLaunch: expect.objectContaining({ urls: ['https://example.test'] }) }))
})
