// Tailwind v4 ships its preset as a PostCSS plugin. Expo bundles
// PostCSS by default and uses lightningcss for prefix handling, so
// autoprefixer is intentionally absent here.
export default {
  plugins: {
    "@tailwindcss/postcss": {},
  },
};
