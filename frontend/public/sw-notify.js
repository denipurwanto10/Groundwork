/*
 * The notification half of the service worker — zone crossing notifications.
 *
 * This file is imported into the service worker that `vite-plugin-pwa`
 * generates (see `workbox.importScripts` in `vite.config.ts`), so the app
 * runs on exactly one worker: the one that caches the map tiles and the one
 * that raises the notifications. Two workers on the same scope would fight
 * over the page.
 *
 * The caching half of the worker is Workbox's: the app shell is precached
 * and the map tiles are kept in a runtime cache. This file only handles the
 * notifications — the page talks to it through
 * `registration.showNotification()`.
 *
 * The `push` handler is already here for the next stage: once the backend
 * sends Web Push messages (VAPID keys), a crossing reaches the operator even
 * when the browser is closed.
 */

/**
 * A notification asked for by the page.
 *
 * The page posts the title and the options; the worker raises them. The
 * worker is the one that survives a backgrounded tab, so this is the path
 * used for every crossing.
 */
self.addEventListener('message', (event) => {
  const data = event.data
  if (!data || data.type !== 'SHOW_NOTIFICATION') return
  const title = typeof data.title === 'string' ? data.title : 'Peringatan Zona'
  const options = data.options && typeof data.options === 'object' ? data.options : {}
  event.waitUntil(self.registration.showNotification(title, options))
})

/**
 * Web Push (next stage).
 *
 * The backend sends a JSON payload of the shape
 * `{ title, body, tag, url }` once VAPID keys are configured. The payload is
 * optional: a plain text body is still worth showing.
 */
self.addEventListener('push', (event) => {
  let payload = {}
  try {
    payload = event.data ? event.data.json() : {}
  } catch {
    payload = { body: event.data ? event.data.text() : '' }
  }
  const title = payload.title || 'Peringatan Zona'
  const options = {
    body: payload.body || '',
    icon: payload.icon || '/favicon.svg',
    badge: payload.badge || '/favicon.svg',
    tag: payload.tag || 'groundwork-push',
    renotify: false,
    requireInteraction: false,
    data: { url: payload.url || '/', kind: 'push' },
  }
  event.waitUntil(self.registration.showNotification(title, options))
})

/**
 * The operator clicks the notification: the app comes back to the front.
 * An already open tab is reused, so the map does not open twice.
 */
self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const target = event.notification.data && event.notification.data.url
  event.waitUntil(
    (async () => {
      const windows = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      for (const client of windows) {
        if ('focus' in client) {
          await client.focus()
          return
        }
      }
      if (self.clients.openWindow) await self.clients.openWindow(target || '/')
    })(),
  )
})
