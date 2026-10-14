import { defineConfig, type Plugin } from "vite";
import preact from "@preact/preset-vite";
import tailwindcss from "@tailwindcss/postcss";
import { VitePWA } from "vite-plugin-pwa";

// The largest file the service worker precaches, in megabytes.
const MB = 1024 * 1024;
const PRECACHE_MAX_MB = 3;

// onnxruntime's bundle names its WebAssembly with new URL(..., import.meta.url), so Vite copies the
// 14 MB file into the build, and from there into every binary. The local voices' worker never
// fetches that copy: it hands the runtime the WebAssembly the gateway downloaded and checked
// (env.wasm.wasmBinary). This drops it from the output.
const dropOrtWasm = (): Plugin => ({
  name: "motita-drop-ort-wasm",
  apply: "build",
  generateBundle(_, bundle) {
    for (const name of Object.keys(bundle)) {
      if (/ort-wasm[^/]*\.wasm$/.test(name)) {
        delete bundle[name];
      }
    }
  },
});

// The build output goes directly into the Go embed directory.
// go:embed in internal/webui picks up everything under assets/.
export default defineConfig({
  // The local voices' worker is a module (it imports onnxruntime and the phonemizer).
  worker: {
    format: "es",
    plugins: () => [dropOrtWasm()],
  },
  plugins: [
    dropOrtWasm(),
    preact(),
    // PWA with injectManifest: a hand-written ~2 KB SW instead of
    // Workbox's ~50 KB generated one.
    VitePWA({
      registerType: "autoUpdate",
      strategies: "injectManifest",
      srcDir: "src",
      filename: "sw.ts",
      // The emoji set is 1285 WebP files, and the generated precache manifest listed every
      // one of them: install would have pulled several MB (and the whole set into the
      // cache) before the page was usable. They are cached on first use instead — the
      // service worker's fetch handler stores what it serves — so an answer that shows one
      // gets it from the cache afterwards, and an install pays nothing for the set.
      injectManifest: {
        globPatterns: ["**/*.{js,css,html,svg,webp,woff2,webmanifest}"],
        // `index.html` is excluded, and it is the difference between a deploy that lands and one
        // that does not. The document names the bundle by hash, so a precached copy keeps naming
        // the PREVIOUS bundle: the reader reloads after a fix and runs the old build. Worse, it
        // is served at `/` and not at `/index.html`, so listing it made `cache.addAll` reject the
        // whole install on that single 404 and no new worker ever activated. Measured against a
        // running gateway: 84 entries, `index.html` the only 404, and the cache left empty.
        globIgnores: ["emoji/**", "index.html"],
        maximumFileSizeToCacheInBytes: PRECACHE_MAX_MB * MB,
      },
      manifest: {
        name: "Motita",
        short_name: "Motita",
        description: "The Motita agent interface",
        theme_color: "#0a0a0f",
        background_color: "#0a0a0f",
        display: "standalone",
        start_url: "/",
        icons: [
          {
            src: "/icon.svg",
            sizes: "any",
            type: "image/svg+xml",
            purpose: "any",
          },
          // PNGs too: Android's install prompt and the notifications want a raster icon.
          {
            src: "/icon-192.png",
            sizes: "192x192",
            type: "image/png",
            purpose: "any",
          },
          {
            src: "/icon-512.png",
            sizes: "512x512",
            type: "image/png",
            purpose: "any",
          },
        ],
      },
    }),
  ],
  css: {
    postcss: {
      plugins: [tailwindcss()],
    },
  },
  build: {
    // Output into the Go embed directory. Vite creates an assets/ subdir
    // for hashed JS/CSS, which go:embed picks up recursively.
    outDir: "../internal/webui/assets",
    emptyOutDir: true,
    // esbuild is smaller and faster than terser; tree-shakes better too.
    minify: "esbuild",
    // Target modern browsers — no polyfills for older engines.
    target: "es2020",
    // No sourcemaps in production — they'd be embedded bytes.
    sourcemap: false,
    reportCompressedSize: true,
    rollupOptions: {
      output: {
        entryFileNames: "assets/[name]-[hash].js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash].[ext]",
      },
    },
  },
  // Preact alias: if any dependency does `import React from 'react'`,
  // it resolves to preact/compat (~2 KB) instead of failing.
  resolve: {
    alias: {
      react: "preact/compat",
      "react-dom": "preact/compat",
    },
  },
});
