import { useEffect, useRef, useState } from 'react'
import { IconBell } from './Icons'
import {
  notificationPermission,
  notifyTest,
  registerServiceWorker,
  requestNotificationPermission,
  type NotifyPermission,
} from '../lib/notifications'

interface Props {
  /** Reports the outcome of the desktop-notification actions in the toast. */
  onNotice?: (kind: 'success' | 'error', text: string) => void
}

/**
 * The bell in the top bar: the notification centre of the app.
 *
 * The notifications that land here are the ones of the fleet — a unit that
 * departs, a unit that arrives. They stay until the operator has read them,
 * because a warning that disappears on its own is a warning nobody saw.
 *
 * The bell also asks for the permission to show desktop notifications, so a
 * notification is reported even when the tab sits in the background. That
 * permission is never asked for on its own: the browser only accepts the
 * request from a user gesture, so the button inside the panel is what unlocks
 * it.
 */
export default function NotificationBell({ onNotice }: Props) {
  // Read once while rendering: the browser answers with the permission it
  // already granted, so a reload does not ask for it again.
  const [permission, setPermission] = useState<NotifyPermission>(() => notificationPermission())
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    // The worker is registered right away: registering it does not need the
    // permission, and having it ready means the first notification is shown
    // without waiting for a second registration.
    void registerServiceWorker()

    // The operator can change the permission in the browser settings while
    // the page stays open, so the button re-reads it when the page returns.
    const sync = () => setPermission(notificationPermission())
    window.addEventListener('focus', sync)
    return () => window.removeEventListener('focus', sync)
  }, [])

  // The panel closes when the operator clicks somewhere else, and Escape
  // closes it as well.
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent | TouchEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('touchstart', onDown)
    window.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('touchstart', onDown)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  async function enable() {
    setBusy(true)
    try {
      const result = await requestNotificationPermission()
      setPermission(result)
      if (result === 'granted') {
        onNotice?.('success', 'Notifikasi desktop aktif. Peringatan akan muncul di pojok layar.')
        // A test notification proves that the desktop really shows them.
        void notifyTest()
      } else if (result === 'denied') {
        onNotice?.('error', 'Notifikasi ditolak. Izinkan notifikasi untuk situs ini di pengaturan browser.')
      } else {
        // The prompt was dismissed without a choice: nothing was granted.
        onNotice?.('error', 'Izin notifikasi belum diberikan.')
      }
    } finally {
      setBusy(false)
    }
  }

  async function sendTest() {
    await notifyTest()
    onNotice?.('success', 'Notifikasi tes dikirim — cek pojok kanan bawah layar.')
  }

  const denied = permission === 'denied'
  const label =
    permission === 'granted'
      ? 'Notifications — desktop notifications are on'
      : denied
        ? 'Notifications — desktop notifications are blocked in the browser settings'
        : 'Notifications — desktop notifications are off'

  return (
    <div className="nb-wrap" ref={rootRef}>
      <button
        type="button"
        className={`icon-btn nb${permission === 'granted' ? ' on' : ''}`}
        onClick={() => setOpen((o) => !o)}
        aria-label="Notifications"
        aria-expanded={open}
        aria-haspopup="dialog"
        title={label}
        data-state={permission}
      >
        <IconBell />
        {permission === 'default' && <i className="nb-dot" aria-hidden="true" />}
      </button>

      {open && (
        <div className="nb-panel" role="dialog" aria-label="Notifications">
          <div className="nb-head">
            <span>Notifications</span>
          </div>

          <p className="nb-empty">No notifications yet.</p>

          <div className="nb-foot">
            {permission === 'granted' ? (
              <button type="button" className="nb-link" onClick={() => void sendTest()}>
                Send a test notification
              </button>
            ) : (
              <button
                type="button"
                className="nb-link"
                onClick={() => void enable()}
                disabled={denied || busy}
              >
                {denied ? 'Blocked in the browser settings' : 'Enable desktop notifications'}
              </button>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
