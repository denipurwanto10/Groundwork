/**
 * Desktop notifications.
 *
 * The toast inside the page tells the operator what happened, but it is gone
 * as soon as the tab loses focus. A system notification (the toast Windows and
 * macOS show in the corner of the screen) survives that, so a notification is
 * reported twice: in the page and on the desktop.
 *
 * Two ways to raise one:
 *
 * - `ServiceWorkerRegistration.showNotification()` — the reliable one. It
 *   works while the tab sits in the background and it is the only way to keep
 *   showing anything once the browser decides to freeze a hidden page.
 * - `new Notification()` — the fallback for browsers without a service
 *   worker (or when the registration failed).
 *
 * The service worker itself lives in `public/sw-notify.js`; it is imported
 * into the worker that `vite-plugin-pwa` generates, so the app runs on one
 * worker: the one that caches the map tiles and the one that notifies.
 */

import { api, type VapidPublicKey } from './api'

/** What the browser is willing to do with notifications. */
export type NotifyPermission = 'unsupported' | 'default' | 'granted' | 'denied'

/**
 * The permission the browser currently grants.
 *
 * 'unsupported' covers the two cases where the API is missing entirely: an
 * old browser, and a page that is not served from a secure context (the API
 * is only available over HTTPS or on localhost).
 */
export function notificationPermission(): NotifyPermission {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  return Notification.permission
}

/** True when a notification would actually be shown right now. */
export function canNotify(): boolean {
  return notificationPermission() === 'granted'
}

let registration: Promise<ServiceWorkerRegistration | null> | null = null

/**
 * Registers the service worker that shows the notifications.
 *
 * The promise is kept, so the browser is asked once per page load no matter
 * how many notifications arrive. A registration that fails is remembered as a
 * failure too: retrying it on every notification would only fill the console
 * with the same error.
 */
export function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (registration) return registration
  registration = (async () => {
    if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return null
    // Service workers only exist in a secure context. On plain http:// (and
    // not localhost) there is nothing to register, so the caller falls back to
    // the plain Notification constructor.
    if (!window.isSecureContext) return null
    try {
      // The PWA plugin registers the worker that also caches the map tiles.
      // Reusing that one keeps the app on a single worker: two workers on
      // the same scope fight over the page.
      const existing = await navigator.serviceWorker.getRegistration('/')
      if (existing) return existing
      // The worker is only served by a production build; the dev server
      // hands out the app without one.
      if (import.meta.env.DEV) return null
      return await navigator.serviceWorker.register('/sw.js', { scope: '/' })
    } catch {
      return null
    }
  })()
  return registration
}

/**
 * Asks the browser for the permission to show notifications.
 *
 * This must run from a user gesture: Firefox refuses the request otherwise
 * and Chrome shows a prompt that no longer counts as a gesture. The button in
 * the top bar is therefore the only caller.
 */
export async function requestNotificationPermission(): Promise<NotifyPermission> {
  if (typeof window === 'undefined' || !('Notification' in window)) return 'unsupported'
  if (Notification.permission !== 'default') return Notification.permission
  try {
    return await Notification.requestPermission()
  } catch {
    return Notification.permission
  }
}

/**
 * Shows the notification through the service worker, or through the plain
 * constructor when there is no worker.
 */
async function showNotice(title: string, options: NotificationOptions): Promise<void> {
  const sw = await registerServiceWorker()
  if (sw) {
    try {
      await sw.showNotification(title, options)
      return
    } catch {
      /* fall back to the plain constructor below */
    }
  }

  try {
    new Notification(title, options)
  } catch {
    /* the browser refused to show it */
  }
}

/**
 * Shows a test notification, so the operator can check that the desktop
 * really shows them (and that the permission was granted at all).
 */
export async function notifyTest(): Promise<void> {
  if (!canNotify()) return
  await showNotice('Notifikasi aktif', {
    body: 'Notifikasi desktop aktif. Kabar kendaraan yang berangkat atau singgah akan muncul di sini.',
    icon: '/favicon.svg',
    badge: '/favicon.svg',
    tag: 'groundwork-test',
    data: { url: '/', kind: 'test' },
  })
}

/**
 * The state of the Web Push feature in this browser.
 *
 * - `unsupported` — the browser has no PushManager, or the service worker is
 *   not there (it only exists in a production build).
 * - `off` — the browser is ready, but there is no subscription yet.
 * - `not-configured` — the server has no VAPID keys, so it sends nothing.
 * - `active` — the browser is subscribed: the messages arrive even when the
 *   tab of the app is closed.
 * - `error` — something went wrong; the message says what.
 */
export type PushStatus = 'unsupported' | 'off' | 'not-configured' | 'active' | 'error'

/** What the subscription attempt came down to. */
export interface PushResult {
  status: PushStatus
  /** The sentence the panel shows under the status. */
  message: string
}

/**
 * Turns a base64url key into the bytes the browser wants.
 *
 * The public key of the server travels as base64url (the text form of the
 * key), while `applicationServerKey` wants the raw bytes.
 */
export function urlBase64ToUint8Array(base64String: string): Uint8Array<ArrayBuffer> {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')

  const raw = atob(base64)
  const output = new Uint8Array(new ArrayBuffer(raw.length))
  for (let i = 0; i < raw.length; i += 1) {
    output[i] = raw.charCodeAt(i)
  }
  return output
}

/**
 * Subscribes this browser to the Web Push messages of the server.
 *
 * Nothing here throws: the panel of the bell shows what happened, and a
 * browser that cannot do Web Push is a normal thing, not a failure.
 *
 * The steps, and where each of them can stop:
 *
 * 1. the permission has to be granted already — the bell asks for it, and
 *    the browser only accepts that question from a click;
 * 2. the service worker has to be registered — it only exists in a
 *    production build, never in the dev server;
 * 3. the server has to have a VAPID key pair — without one it sends
 *    nothing at all;
 * 4. the browser subscribes with the public key, and the subscription is
 *    handed to the server, which keeps it.
 *
 * A browser that is already subscribed keeps its subscription: only the
 * server is told about it again, so a renewed key never breaks the old one.
 */
export async function subscribeToPush(): Promise<PushResult> {
  if (typeof window === 'undefined' || !('Notification' in window)) {
    return { status: 'unsupported', message: 'This browser does not support notifications.' }
  }
  if (Notification.permission !== 'granted') {
    return { status: 'off', message: 'The permission for notifications is not given yet.' }
  }
  if (!('serviceWorker' in navigator) || !('PushManager' in window)) {
    return { status: 'unsupported', message: 'This browser does not support Web Push.' }
  }

  const sw = await registerServiceWorker()
  if (!sw || !sw.pushManager) {
    return {
      status: 'unsupported',
      message: 'The service worker is not registered yet — Web Push needs a production build.',
    }
  }

  let key: VapidPublicKey
  try {
    key = await api.getVapidPublicKey()
  } catch {
    return { status: 'error', message: 'The backend cannot be reached.' }
  }
  if (!key.enabled || !key.public_key) {
    return { status: 'not-configured', message: 'Web Push is not configured on the server yet.' }
  }

  try {
    // A browser that is already subscribed keeps its subscription: asking
    // again would hand a new endpoint to the server and lose the old one.
    const existing = await sw.pushManager.getSubscription()
    const subscription =
      existing ??
      (await sw.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlBase64ToUint8Array(key.public_key),
      }))

    await api.subscribePush(subscription.toJSON())
    return { status: 'active', message: 'Web Push is on — the messages arrive even when this tab is closed.' }
  } catch {
    return { status: 'error', message: 'The browser refused the Web Push subscription.' }
  }
}

/**
 * Removes the subscription of this browser from the server.
 *
 * A browser that never subscribed has nothing to remove, so the call is
 * skipped and the result stays quiet.
 */
export async function unsubscribeFromPush(): Promise<void> {
  if (typeof window === 'undefined' || !('serviceWorker' in navigator)) return

  try {
    const sw = await registerServiceWorker()
    if (!sw || !sw.pushManager) return

    const subscription = await sw.pushManager.getSubscription()
    if (!subscription) return

    await api.unsubscribePush(subscription.endpoint)
    await subscription.unsubscribe()
  } catch {
    /* the browser or the server refused; the panel keeps its state */
  }
}
