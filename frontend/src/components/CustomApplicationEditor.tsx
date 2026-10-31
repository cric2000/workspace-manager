import { useState } from 'react'
import type { CustomApplication } from '../../bindings/workspace-manager/internal/domain/models'

export function CustomApplicationEditor({ initial, disabled, onSave, onCancel }: {
  initial?: CustomApplication; disabled: boolean; onSave: (value: CustomApplication) => void; onCancel: () => void
}) {
  const [value, setValue] = useState<Omit<CustomApplication, 'arguments'> & { arguments: string[] }>(() => initial ? { ...initial, arguments: [...(initial.arguments ?? [])] } : { id: '', name: '', executable: '', workingDirectory: '', arguments: [], instanceBusName: '' })
  const [error, setError] = useState('')
  return <form className="editor-fields custom-application-form" aria-label="Custom application configuration" onSubmit={event => {
    event.preventDefault()
    if (disabled) return
    if (!value.name.trim() || !value.executable.startsWith('/') || !value.workingDirectory.startsWith('/')) { setError('Enter a name and absolute executable and working-directory paths.'); return }
    setError(''); onSave({ ...value, name: value.name.trim() })
  }}>
    <h3>{initial ? 'Edit custom application' : 'Add custom application'}</h3>
    <p className="draft-note">Custom tools open without a terminal. Configure a terminal emulator explicitly if required.</p>
    <label htmlFor="custom-name">Name<input autoFocus id="custom-name" required disabled={disabled} value={value.name} onChange={event => setValue({ ...value, name: event.target.value })} /></label>
    <label htmlFor="custom-executable">Executable path<input id="custom-executable" required disabled={disabled} placeholder="/absolute/path/to/application" value={value.executable} onChange={event => setValue({ ...value, executable: event.target.value })} /></label>
    <label htmlFor="custom-directory">Working directory<input id="custom-directory" required disabled={disabled} placeholder="/absolute/path/to/directory" value={value.workingDirectory} onChange={event => setValue({ ...value, workingDirectory: event.target.value })} /></label>
    <fieldset disabled={disabled}><legend>Arguments</legend><p className="muted text-xs">Each row is one literal argument. Keep spaces inside a row; do not add shell quotes. An empty row passes an empty argument.</p>
      {value.arguments.map((argument, index) => <div className="argument-row" key={index}><label>Argument {index + 1}<input value={argument} onChange={event => setValue({ ...value, arguments: value.arguments.map((old, position) => position === index ? event.target.value : old) })} /></label><button type="button" className="secondary-button" aria-label={`Remove argument ${index + 1}`} onClick={() => setValue({ ...value, arguments: value.arguments.filter((_, position) => position !== index) })}>Remove</button></div>)}
      <button type="button" className="secondary-button" disabled={value.arguments.length >= 128} onClick={() => setValue({ ...value, arguments: [...value.arguments, ''] })}>Add argument</button>
    </fieldset>
    <label htmlFor="custom-bus-name">D-Bus service identity <span className="muted">(optional)</span><input id="custom-bus-name" disabled={disabled} placeholder="org.example.Application" value={value.instanceBusName} onChange={event => setValue({ ...value, instanceBusName: event.target.value })} /></label>
    <p className="draft-note">Use only an identity documented by the application. A service owner can confirm this identity, but cannot prove a particular window or project is open. Leave blank if unknown.</p>
    {error && <p className="error-panel" role="alert">{error}</p>}
    <div className="dialog-actions"><button type="button" className="secondary-button" disabled={disabled} onClick={onCancel}>Discard tool changes</button><button className="primary-button" type="submit" disabled={disabled}>Keep in draft</button></div>
  </form>
}
