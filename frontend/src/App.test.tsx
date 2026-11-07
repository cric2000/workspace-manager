import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import App from './App'

vi.mock('./pages/Workspaces', () => ({ Workspaces: () => <h1>Workspaces</h1> }))
vi.mock('./pages/Projects', () => ({ Projects: ({ createRequest }: { createRequest?: number }) => <h1>{createRequest ? 'New project form' : 'Projects'}</h1> }))
vi.mock('./pages/Applications', () => ({ Applications: () => <h1>App library</h1> }))
vi.mock('./pages/Diagnostics', () => ({ Diagnostics: () => <h1>Diagnostics</h1> }))
vi.mock('./pages/Lifecycle', () => ({ Lifecycle: ({ page }: { page: string }) => <h1>{page}</h1> }))

describe('task navigation', () => {
  it('starts on Workspaces with only three primary destinations', () => {
    render(<App />)
    const navigation = within(screen.getByRole('navigation', { name: 'Main navigation' }))
    expect(navigation.getAllByRole('button').map(button => button.textContent)).toEqual(['Workspaces', 'Projects', 'Saved sessions'])
    expect(navigation.getByRole('button', { name: 'Workspaces' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('heading', { name: 'Workspaces' })).toBeVisible()
    expect(screen.queryByRole('button', { name: 'Dashboard' })).not.toBeInTheDocument()
  })
  it('keeps app browsing and diagnostics under Settings with a clear way back', async () => {
    const user = userEvent.setup()
    render(<App />)
    expect(screen.queryByRole('button', { name: 'App library' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Settings' }))
    await user.click(screen.getByRole('button', { name: 'App library' }))
    expect(screen.getByRole('heading', { name: 'App library' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Settings' })).toHaveAttribute('aria-current', 'page')
    await user.click(screen.getByRole('button', { name: '← Back to settings' }))
    await user.click(screen.getByRole('button', { name: 'Diagnostics' }))
    expect(screen.getByRole('heading', { name: 'Diagnostics' })).toBeVisible()
  })
  it('opens creation directly through keyboard quick actions', async () => {
    const user = userEvent.setup()
    render(<App />)
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true })
    await user.type(screen.getByRole('textbox', { name: 'Search actions' }), 'create project')
    await user.keyboard('{Enter}')
    expect(screen.getByRole('heading', { name: 'New project form' })).toBeVisible()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
