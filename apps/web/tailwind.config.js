/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Tunnex brand kit. Backgrounds are near-black with a faint violet bias
        // (chosen to sit under the iris accent, not a pure grey). The <Logo> in
        // src/brand.tsx + these tokens are the whole brand surface.
        ink: {
          950: "#08080d", // deepest (overlays/scrim base)
          900: "#0b0b12", // app background
          800: "#12121c", // card / container
          700: "#1a1a28", // raised layer / control background
          600: "#232335", // subtle borders / dividers
        },
        // Accent — electric iris. 500 is the brand anchor (#7C5CFF).
        accent: {
          400: "#9b84ff", // hover / active / focus glow
          500: "#7c5cff", // primary CTA
          600: "#6344e6", // pressed
        },
        // Semantic status — RESERVED, deliberately distinct from the accent hue so
        // "connected / caution / revoked" never reads as a brand highlight.
        ok: "#34d399", // online / healthy (emerald)
        warn: "#fbbf24", // caution / one-time secret (amber)
        danger: "#fb7185", // revoked / error (rose)
      },
      fontFamily: {
        // Inter for UI/body; JetBrains Mono is FIRST-CLASS for the things a VPN
        // admin reads character-by-character: IPs, public keys, config files.
        sans: ['"Inter Variable"', "ui-sans-serif", "system-ui", "Segoe UI", "Roboto", "sans-serif"],
        mono: ['"JetBrains Mono Variable"', "ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [],
};
