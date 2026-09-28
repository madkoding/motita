import { defineConfig } from 'vite'
import preact from '@preact/preset-vite'
import tailwindcss from 'tailwindcss'
import autoprefixer from 'autoprefixer'
import { VitePWA } from 'vite-plugin-pwa'

// The build output goes directly into the Go embed directory.
// go:embed in internal/webui picks up everything under assets/.
export default defineConfig({
  plugins: [
    preact(),
    // PWA with injectManifest: a hand-written ~2 KB SW instead of
    // Workbox's ~50 KB generated one.
    VitePWA({
      registerType: 'autoUpdate',
      strategies: 'injectManifest',
      srcDir: 'src',
      filename: 'sw.ts',
      // The emoji set is 1285 WebP files, and the generated precache manifest listed every
      // one of them: install would have pulled several MB (and the whole set into the
      // cache) before the page was usable. They are cached on first use instead — the
      // service worker's fetch handler stores what it serves — so an answer that shows one
      // gets it from the cache afterwards, and an install pays nothing for the set.
      injectManifest: {
        globPatterns: ['**/*.{js,css,html,svg,webp,woff2,webmanifest}'],
        // `index.html` is excluded, and it is the difference between a deploy that lands and one
        // that does not. The document names the bundle by hash, so a precached copy keeps naming
        // the PREVIOUS bundle: the reader reloads after a fix and runs the old build. Worse, it
        // is served at `/` and not at `/index.html`, so listing it made `cache.addAll` reject the
        // whole install on that single 404 and no new worker ever activated. Measured against a
        // running gateway: 84 entries, `index.html` the only 404, and the cache left empty.
        globIgnores: ['emoji/**', 'index.html'],
        maximumFileSizeToCacheInBytes: 3 * 1024 * 1024,
      },
      manifest: {
        name: 'Motita',
        short_name: 'Motita',
        description: 'The Motita agent interface',
        theme_color: '#0a0a0f',
        background_color: '#0a0a0f',
        display: 'standalone',
        start_url: '/',
        icons: [
          { src: '/icon.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' }
        ]
      }
    })
  ],
  css: {
    postcss: {
      plugins: [tailwindcss(), autoprefixer()]
    }
  },
  build: {
    // Output into the Go embed directory. Vite creates an assets/ subdir
    // for hashed JS/CSS, which go:embed picks up recursively.
    outDir: '../internal/webui/assets',
    emptyOutDir: true,
    // esbuild is smaller and faster than terser; tree-shakes better too.
    minify: 'esbuild',
    // Target modern browsers — no polyfills for older engines.
    target: 'es2020',
    // No sourcemaps in production — they'd be embedded bytes.
    sourcemap: false,
    reportCompressedSize: true,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash].[ext]'
      }
    }
  },
  // Preact alias: if any dependency does `import React from 'react'`,
  // it resolves to preact/compat (~2 KB) instead of failing.
  resolve: {
    alias: {
      'react': 'preact/compat',
      'react-dom': 'preact/compat'
    }
  }
})