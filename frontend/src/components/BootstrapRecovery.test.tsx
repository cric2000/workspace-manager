import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { BootstrapRecovery } from './BootstrapRecovery'
import { checkBootstrapRecovery, commitRecoveryRegistration, getRecoveryRegistrationOutcome, reviewRecoveryRegistration, type BootstrapRecoveryReport, type RecoveryRegistrationReview } from '../services/projects'
import { ProjectSource } from '../../bindings/workspace-manager/internal/domain/models'

vi.mock('../services/projects', () => ({ checkBootstrapRecovery: vi.fn(), commitRecoveryRegistration: vi.fn(), getRecoveryRegistrationOutcome: vi.fn(), reviewRecoveryRegistration: vi.fn() }))
const target = { scope: 'creation', projectId: '1'.repeat(32), logId: '2'.repeat(32) }
const timestamp = '2026-11-02T01:00:00Z'
const report: BootstrapRecoveryReport = { owner: 'released', path: '/code/example', canRestoreRegistration: true, saved: true, message: 'Current files match the saved starter.', assessment: { checkedAt: timestamp, destination: 'directory', registration: 'missing', files: 'matching', setupOutput: 'not_applicable', message: 'Current files match.' } }
const review: RecoveryRegistrationReview = { token: 'recovery-token', expiresAt: timestamp, project: { id: target.projectId, name: 'example', path: report.path, source: ProjectSource.SourceBootstrapped, createdAt: timestamp, updatedAt: timestamp, commands: [{ id: '3'.repeat(32), configuration: { name: 'Run Go application', executable: '/tools/go', arguments: ['run','.'], workingDirectory: report.path } }] } }
const restored = { committed: true, state: { projects: [] }, code: '', message: 'Registration restored. Startup commands remain unapproved.' }
beforeEach(() => {
  vi.mocked(checkBootstrapRecovery).mockReset().mockResolvedValue({ report, code: '', message: '' })
  vi.mocked(reviewRecoveryRegistration).mockReset().mockResolvedValue({ review, code: '', message: '' })
  vi.mocked(commitRecoveryRegistration).mockReset().mockResolvedValue(restored)
  vi.mocked(getRecoveryRegistrationOutcome).mockReset().mockResolvedValue(restored)
})
it('requires explicit inspection, a fresh exact configuration review and a separate restore', async () => {
  const onRestored = vi.fn(), busy = vi.fn()
  render(<BootstrapRecovery target={target} disabled={false} onBusyChange={busy} onRestored={onRestored} />)
  const user = userEvent.setup()
  expect(checkBootstrapRecovery).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Check recovery' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Registrationmissing')
  expect(commitRecoveryRegistration).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Review registration recovery' }))
  expect(await screen.findByRole('generic', { name: 'Recovered registration review' })).toHaveTextContent('/tools/go')
  expect(screen.getByRole('generic', { name: 'Recovered registration review' })).toHaveTextContent('"run"')
  expect(commitRecoveryRegistration).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Restore registration' }))
  expect(await screen.findByText('Registration restored. Startup commands remain unapproved.')).toBeInTheDocument()
  expect(commitRecoveryRegistration).toHaveBeenCalledExactlyOnceWith('recovery-token', expect.any(AbortSignal))
  expect(onRestored).toHaveBeenCalledOnce()
  expect(busy).toHaveBeenLastCalledWith(false)
})
it.each(['active','untracked'])('keeps %s operations out of registration restore', async owner => {
  vi.mocked(checkBootstrapRecovery).mockResolvedValueOnce({ report: { ...report, owner, assessment: undefined, canRestoreRegistration: false, saved: false, message: 'Inspect this attempt manually.' }, code: '', message: '' })
  render(<BootstrapRecovery target={target} disabled={false} onBusyChange={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: 'Check recovery' }))
  expect(await screen.findByRole('status')).toHaveTextContent('Inspect this attempt manually')
  expect(screen.queryByRole('button', { name: 'Review registration recovery' })).not.toBeInTheDocument()
  expect(reviewRecoveryRegistration).not.toHaveBeenCalled()
})
it('queries a lost restore response without issuing another metadata write', async () => {
  vi.mocked(commitRecoveryRegistration).mockRejectedValueOnce(new Error('private raw detail'))
  vi.mocked(getRecoveryRegistrationOutcome).mockResolvedValueOnce({ committed: false, state: null, code: 'recovery_pending', message: 'No final outcome yet.' }).mockResolvedValueOnce(restored)
  render(<BootstrapRecovery target={target} disabled={false} onBusyChange={vi.fn()} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Check recovery' }))
  await user.click(await screen.findByRole('button', { name: 'Review registration recovery' }))
  await user.click(await screen.findByRole('button', { name: 'Restore registration' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('restore response is unavailable')
  expect(screen.getByRole('alert')).not.toHaveTextContent('private raw detail')
  await user.click(screen.getByRole('button', { name: 'Check restore outcome' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('No final outcome yet')
  await user.click(screen.getByRole('button', { name: 'Check restore outcome' }))
  expect(await screen.findByText('Registration restored. Startup commands remain unapproved.')).toBeInTheDocument()
  expect(commitRecoveryRegistration).toHaveBeenCalledTimes(1)
  expect(getRecoveryRegistrationOutcome).toHaveBeenCalledTimes(2)
})
it('cancels inspection on unmount and never requests restoration after a failed check', async () => {
  let reject!: (error: Error) => void
  vi.mocked(checkBootstrapRecovery).mockReturnValueOnce(new Promise((_, fail) => { reject = fail }))
  const view = render(<BootstrapRecovery target={target} disabled={false} onBusyChange={vi.fn()} />)
  await userEvent.click(screen.getByRole('button', { name: 'Check recovery' }))
  await userEvent.click(screen.getByRole('button', { name: 'Cancel recovery request' }))
  expect(vi.mocked(checkBootstrapRecovery).mock.calls[0][1].aborted).toBe(true)
  await act(async () => reject(new Error('cancelled')))
  expect(await screen.findByRole('alert')).toHaveTextContent('Existing files and registrations were kept')
  expect(commitRecoveryRegistration).not.toHaveBeenCalled()
  view.unmount()
})
