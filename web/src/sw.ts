/// <reference lib="webworker" />

// Minimal service worker: cache-first for the shell, network-only for /v1/*.
// This is ~2 KB minified vs. ~50 KB for Workbox's generated SW.

const SHELL_CACHE = 'motita-shell-v18'

// Shell assets to pre-cache on install. The PWA plugin injects the actual
// build manifest entries here via self.__WB_MANIFEST.
const precacheAssets: string[] = (self as any).__WB_MANIFEST?.map(
  (e: any) => e.url
) ?? ['/']

self.addEventListener('install', (event: ExtendableEvent) => {
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) => cache.addAll(precacheAssets))
  )
  self.skipWaiting()
})

self.addEventListener('activate', (event: ExtendableEvent) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== SHELL_CACHE).map((k) => caches.delete(k)))
    )
  )
  self.clients.claim()
})

self.addEventListener('fetch', (event: FetchEvent) => {
  const url = new URL(event.request.url)

  // Never cache API calls — the gateway is the source of truth.
  if (url.pathname.startsWith('/v1/')) {
    return
  }

  // Cache-first for everything else (the shell).
  event.respondWith(
    caches.match(event.request).then((cached) => {
      if (cached) return cached
      return fetch(event.request).then((response) => {
        const clone = response.clone()
        caches.open(SHELL_CACHE).then((cache) => cache.put(event.request, clone))
        return response
      })
    })
  )
})