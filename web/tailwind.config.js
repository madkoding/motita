/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  darkMode: 'css',
  theme: {
    extend: {
      colors: {
        accent: '#4cc2ff',
        'accent-glow': 'rgba(76, 194, 255, 0.2)',
        'surface-solid': '#16161e',
        'surface-opaque': '#1a1a22',
        danger: '#ff6b6b',
        warning: '#ffb020'
      },
      fontFamily: {
        sans: ['Sansation', 'system-ui', '-apple-system', 'Segoe UI', 'Roboto', 'sans-serif'],
        mono: ['JetBrainsMonoNF', 'ui-monospace', 'SF Mono', 'Menlo', 'Consolas', 'monospace']
      }
    }
  },
  // Utilities the app does NOT use are disabled, one by one: each saves CSS bytes.
  //
  // Only the LEAF utilities may be disabled here, never a plugin that emits the
  // custom properties its leaves read. Tailwind injects `--tw-backdrop-*: ` only
  // through the `backdropFilter` plugin (`addDefaults("backdrop-filter", ...)`),
  // and its leaves' `backdrop-filter` value lists all nine of those variables. With
  // the plugin off, `backdrop-filter: var(--tw-backdrop-blur) var(--tw-backdrop-brightness)
  // ...` keeps the undefined variables: the whole declaration is invalid at
  // computed-value time and is dropped, so `backdrop-blur-md` renders nothing at
  // all. `transform`, `filter` and `boxShadow` are the same trap (`transform` is
  // also where Tailwind's translate/scale/rotate leaves put their own defaults), so
  // they stay on; dropping them needs the leaves' value lists rewritten first.
  corePlugins: {
    container: false,
    aspectRatio: false,
    textOpacity: false,
    backgroundOpacity: false,
    borderOpacity: false,
    divideOpacity: false,
    gradientColorStops: false,
    tableLayout: false,
    borderCollapse: false,
    borderSpacing: false,
    boxShadowColor: false,
    opacity: false,
    transitionProperty: false,
    transitionDuration: false,
    transitionTimingFunction: false,
    transitionDelay: false,
    animation: false
  },
  plugins: []
}