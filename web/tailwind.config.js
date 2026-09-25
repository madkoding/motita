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
  // Disable plugins the app does NOT use. Each saves CSS bytes.
  corePlugins: {
    container: false,
    aspectRatio: false,
    textOpacity: false,
    backgroundOpacity: false,
    borderOpacity: false,
    divideOpacity: false,
    gradientColorStops: false,
    filter: false,
    backdropFilter: false,
    transform: false,
    tableLayout: false,
    borderCollapse: false,
    borderSpacing: false,
    boxShadow: false,
    opacity: false,
    transitionProperty: false,
    transitionDuration: false,
    transitionTimingFunction: false,
    transitionDelay: false,
    animation: false
  },
  plugins: []
}