import { useLayoutEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'

export function Modal({ title, description, busy, onClose, children, wide = false }: {
  title: string; description: string; busy: boolean; onClose: () => void; children: ReactNode; wide?: boolean
}) {
  const dialog = useRef<HTMLDialogElement>(null)
  useLayoutEffect(() => {
    const element = dialog.current!
    const opener = document.activeElement as HTMLElement | null
    element.showModal()
    element.querySelector<HTMLElement>('[data-dialog-initial-focus]')?.focus()
    return () => {
      element.close()
      // Layout cleanup precedes removal of a deleted card. Recheck its trigger
      // after the DOM commit before choosing where keyboard focus should return.
      queueMicrotask(() => {
        if (document.querySelector('dialog[open]')) return
        const target = opener?.isConnected ? opener : document.querySelector<HTMLElement>('[data-new-workspace]')
        target?.focus()
      })
    }
  }, [])

  return <dialog ref={dialog} className={`workspace-dialog${wide ? ' workspace-dialog-wide' : ''}`} aria-labelledby="dialog-title" aria-describedby="dialog-description"
    onCancel={event => { event.preventDefault(); if (!busy) onClose() }}>
    <div className="dialog-header">
      <div><h2 id="dialog-title">{title}</h2><p id="dialog-description" className="muted">{description}</p></div>
      <button type="button" className="icon-button" aria-label="Close dialog" disabled={busy} onClick={onClose}><X size={18} aria-hidden="true" /></button>
    </div>
    {children}
  </dialog>
}
