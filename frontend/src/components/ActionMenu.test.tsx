import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { ActionMenu } from './ActionMenu'

it('keeps occasional actions out of the main flow and returns focus after selecting one', async () => {
  const action = vi.fn(), user = userEvent.setup()
  render(<ActionMenu name="Coding"><button onClick={action}>Duplicate workspace</button></ActionMenu>)
  expect(screen.queryByRole('button', { name: 'Duplicate workspace' })).not.toBeInTheDocument()
  const trigger = screen.getByText('More')
  await user.click(trigger)
  await user.click(screen.getByRole('button', { name: 'Duplicate workspace' }))
  expect(action).toHaveBeenCalledOnce()
  expect(screen.queryByRole('button', { name: 'Duplicate workspace' })).not.toBeInTheDocument()
  expect(trigger.closest('summary')).toHaveFocus()
})
it('closes on Escape or an outside pointer without running an action', async () => {
  const action = vi.fn(), user = userEvent.setup()
  render(<ActionMenu name="Coding"><button onClick={action}>Delete workspace</button></ActionMenu>)
  const trigger = screen.getByText('More')
  await user.click(trigger)
  await user.keyboard('{Escape}')
  expect(trigger.closest('summary')).toHaveFocus()
  expect(screen.queryByRole('button', { name: 'Delete workspace' })).not.toBeInTheDocument()
  await user.click(trigger)
  fireEvent.pointerDown(document.body)
  expect(screen.queryByRole('button', { name: 'Delete workspace' })).not.toBeInTheDocument()
  expect(action).not.toHaveBeenCalled()
})
