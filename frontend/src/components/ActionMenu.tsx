import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Ellipsis } from 'lucide-react'

export function ActionMenu({ name, children }: { name: string; children: ReactNode }) {
  const element = useRef<HTMLDetailsElement>(null)
  const [open, setOpen] = useState(false)
  useEffect(() => {
    if (!open) return
    const closeOutside = (event: PointerEvent) => {
      if (element.current && !element.current.contains(event.target as Node)) close(false)
    }
    window.addEventListener('pointerdown', closeOutside)
    return () => window.removeEventListener('pointerdown', closeOutside)
  }, [open])
  function close(focus: boolean) {
    if (!element.current) return
    element.current.open = false
    setOpen(false)
    if (focus) element.current.querySelector('summary')?.focus()
  }
  return <details ref={element} className="action-menu" onToggle={event => setOpen(event.currentTarget.open)} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true) } }}>
    <summary onClick={event => { event.preventDefault(); if (element.current) { const next = !element.current.open; element.current.open = next; setOpen(next) } }} aria-expanded={open} aria-label={`More actions for ${name}`} title={`More actions for ${name}`}><Ellipsis size={18} aria-hidden="true" /><span>More</span></summary>
    <div className="action-menu-items" hidden={!open} onClick={event => { if ((event.target as HTMLElement).closest('button')) close(true) }}>{children}</div>
  </details>
}
