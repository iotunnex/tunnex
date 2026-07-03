/** @type {import('tailwindcss').Config} */
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        // Tunnex palette — deep slate ground with a secure-tunnel teal accent.
        // Swap these for the real brand tokens when the logo/brand kit lands (S4.1).
        ink: {
          900: "#0a0e14",
          800: "#0f141c",
          700: "#161d28",
          600: "#1f2836",
        },
        accent: {
          400: "#2dd4bf",
          500: "#14b8a6",
          600: "#0d9488",
        },
      },
      fontFamily: {
        sans: [
          "ui-sans-serif",
          "system-ui",
          "-apple-system",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "sans-serif",
        ],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
    },
  },
  plugins: [],
};
