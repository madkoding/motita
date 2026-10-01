/// <reference lib="webworker" />

// The file is compiled with the DOM library in scope, so a bare `self` resolves to `Window` — which
// has no `skipWaiting`, no `clients`, and none of the service-worker event maps. Declaring it here
// as the worker global is what makes the three `addEventListener` calls below typed correctly
// instead of needing a cast each. It is types only: nothing reaches the bundle.
declare const self: ServiceWorkerGlobalScope

// Minimal service worker: the shell always from the network, everything else cache-first.
// This is ~2 KB minified vs. ~50 KB for Workbox's generated SW.

// Bumping this name is what retires the previous generation of the cache: `activate` deletes every
// cache that is not this one, so a worker that was broken (or served a shell from a stale build)
// does not leave its entries behind for the next one to match.
const SHELL_CACHE = 'motita-shell-v20'

// Shell assets to pre-cache on install. The PWA plugin injects the actual
// build manifest entries here via self.__WB_MANIFEST.
//
// `index.html` is EXCLUDED on purpose, and this is a real bug fix rather than a tidy-up:
//
//   * The document is what names the bundle (`assets/index-<hash>.js`). Cached, it keeps naming
//     the OLD hash, so the reader goes on running the previous build and every fix "does nothing".
//   * The server already sends it with `Cache-Control: no-store`, so a copy of it in the cache is
//     the one thing that outlives the build it came from.
//   * It is not even served at `/index.html` — the page is registered at `/` alone — so precaching
//     it made `cache.addAll` reject the WHOLE install on a 404, leaving the previous worker in
//     place forever. Measured: of 84 precache entries the served worker listed, `index.html` was
//     the only one that 404'd, and the cache stayed empty.
const precacheAssets: string[] = ((self as any).__WB_MANIFEST ?? [])
  .map((e: any) => e.url)
  .filter((url: string) => !isDocument(url))

/** The shell document, under any of the names the build might give it. */
function isDocument(url: string): boolean {
  const path = url.split('?')[0]
  return path === '/' || path === '' || path.endsWith('/index.html') || path === 'index.html'
}

self.addEventListener('install', (event: ExtendableEvent) => {
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) =>
      // One `cache.add` per file instead of a single `cache.addAll`. `addAll` is all-or-nothing:
      // a single entry that 404s rejects the install, the new worker never activates, and the
      // browser keeps serving the previous shell — which is indistinguishable, from the outside,
      // from "the change did not work". Here a single miss costs one file, and the gate
      // (`TestTheServiceWorkerPrecachesOnlyWhatIsServed`) is what forbids the miss.
      Promise.all(
        precacheAssets.map((url) => cache.add(url).catch(() => undefined))
      )
    )
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

  // The document: always the network, with the cache only as an offline fallback.
  //
  // Cache-first here is what turns a deploy into a no-op. The shell is a 2 KB file that names
  // every other asset by hash, so holding it means holding the whole previous build; the reader
  // reloads, gets the same HTML, the same bundle, and the same bug. The server's `no-store` on
  // this file says the same thing, and this is the worker agreeing with it rather than overriding
  // it.
  if (event.request.mode === 'navigate') {
    event.respondWith(
      fetch(event.request)
        .then((response) => {
          // A successful navigation refreshes the offline copy. `response.ok` matters: caching a
          // 404 or a 500 would make the next offline visit show that error as if it were the app.
          if (response.ok) {
            const clone = response.clone()
            caches.open(SHELL_CACHE).then((cache) => cache.put('/', clone))
          }
          return response
        })
        .catch(() => caches.match('/').then((cached) => cached ?? Response.error()))
    )
    return
  }

  // Everything else: cache-first. Safe because the build names these by content hash
  // (`index-DDsmlomn.js`), so a cache hit is by construction the file that was asked for — the
  // name changes whenever the bytes do.
  event.respondWith(
    caches.match(event.request).then((cached) => {
      if (cached) return cached
      return fetch(event.request).then((response) => {
        if (response.ok) {
          const clone = response.clone()
          caches.open(SHELL_CACHE).then((cache) => cache.put(event.request, clone))
        }
        return response
      })
    })
  )
})
