import { useEffect, useRef, useState } from 'react'
import { IconBell } from './Icons'
import {
  notificationPermission,
  notifyTest,
  registerServiceWorker,
  requestNotificationPermission,
  subscribeToPush,
  type NotifyPermission,
  type PushResult,
  type PushStatus,
} from '../lib/notifications'
import { api } from '../lib/api'

interface Props {
  /** Reports the outcome of the desktop-notification actions in the toast. */
  onNotice?: (kind: 'success' | 'error', text: string) => void
}

/**
 * The sentence the panel shows under the Web Push status.
 *
 * The words are plain on purpose: the operator does not have to know what
 * VAPID is to know whether the messages will reach them.
 */
const pushLabel: Record<PushStatus, string> = {
  active: 'On — the messages arrive even when this tab is closed.',
  off: 'Not on yet — allow notifications to turn it on.',
  'not-configured': 'Not configured on the server yet.',
  unsupported: 'This browser does not support Web Push.',
  error: 'Could not be turned on.',
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
 *
 * With the permission the browser can do more: Web Push. The server holds
 * a VAPID key pair of its own, and the browser subscribes to it — so the
 * message of a unit that departs or arrives still arrives when this tab is
 * closed. The panel says which of the two is working.
 */
export default function NotificationBell({ onNotice }: Props) {
  // Read once while rendering: the browser answers with the permission it
  // already granted, so a reload does not ask for it again.
  const [permission, setPermission] = useState<NotifyPermission>(() => notificationPermission())
  const [push, setPush] = useState<PushResult>({ status: 'off', message: pushLabel.off })
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  // The subscription is only looked for once: a second visit does not have
  // to ask the browser again.
  const pushChecked = useRef(false)

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

  // The permission may have been given at an earlier visit: the
  // subscription is refreshed then, quietly. Nothing is asked of the
  // operator — the permission is already there.
  useEffect(() => {
    if (pushChecked.current) return
    if (permission !== 'granted') return
    pushChecked.current = true

    let cancelled = false
    void subscribeToPush().then((result) => {
      if (!cancelled) setPush(result)
    })
    return () => {
      cancelled = true
    }
  }, [permission])

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
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('touchstart', onDown)
      document.removeEventListener('keydown', onKey)
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

        // The permission alone only reaches the browser. The push needs a
        // subscription on the server as well, so the message still arrives
        // when this tab is closed.
        pushChecked.current = true
        const pushResult = await subscribeToPush()
        setPush(pushResult)
        if (pushResult.status === 'active') {
          onNotice?.('success', 'Web Push aktif. Kabar unit yang berangkat atau tiba sampai walau tab ditutup.')
        } else if (pushResult.status === 'not-configured') {
          onNotice?.('error', 'Web Push belum dikonfigurasi di server.')
        }
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

  /**
   * The test of the whole push chain: the server sends a message to the
   * subscriptions of this account, and the browser raises it.
   */
  async function sendPushTest() {
    setBusy(true)
    try {
      const result = await api.sendTestPush()
      if (result.sent > 0) {
        onNotice?.('success', `Web Push tes dikirim ke ${result.sent} perangkat — cek notifikasi sistem.`)
      } else if (!result.enabled) {
        onNotice?.('error', 'Web Push belum dikonfigurasi di server.')
      } else {
        onNotice?.('error', 'Belum ada perangkat terdaftar untuk push. Izinkan notifikasi dulu.')
      }
    } catch {
      onNotice?.('error', 'Web Push tes gagal dikirim.')
    } finally {
      setBusy(false)
    }
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

          <div className="nb-push">
            <span className={`nb-push-dot ${push.status}`} aria-hidden="true" />
            <div className="nb-push-text">
              <strong>Web Push</strong>
              <span>{push.message}</span>
            </div>
          </div>

          <div className="nb-foot">
            <div className="nb-foot-actions">
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
              {permission === 'granted' && (
                <button
                  type="button"
                  className="nb-link"
                  onClick={() => void sendPushTest()}
                  disabled={busy}
                >
                  Send a Web Push test
                </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
