import { useCallback, useEffect, useMemo, useState } from 'react'
import { AppWindow } from 'lucide-react'
import { getApplicationIcon } from '../services/applications'

type IconLoader = (id: string) => Promise<string>

// One cache per catalogue attempt, shared by picker rows and selected entries.
export function useApplicationIcons(attempt: number): IconLoader {
  const icons = useMemo(() => new Map<string, Promise<string>>(), [attempt])
  return useCallback(id => {
    let request = icons.get(id)
    if (!request) {
      request = getApplicationIcon(id).catch(() => '')
      icons.set(id, request)
    }
    return request
  }, [icons])
}

export function ApplicationIcon({ id, load }: { id: string; load: IconLoader }) {
  const [url, setURL] = useState('')
  useEffect(() => {
    let current = true
    load(id).then(value => { if (current && value.startsWith('data:image/png;base64,')) setURL(value) })
    return () => { current = false }
  }, [id, load])
  return <span className="application-icon" aria-hidden="true">{url ? <img src={url} alt="" width={36} height={36} onError={() => setURL('')} /> : <AppWindow size={23} />}</span>
}
