import type { Config } from "tailwindcss"

const config: Config = {
  content: ["./src/**/*.{js,ts,jsx,tsx,mdx}"],
  theme: {
    extend: {
      colors: {
        bg: "#080B10",
        surface: "#0F1318",
        "surface-raised": "#161C2B",
        border: "#1C2333",
        accent: {
          DEFAULT: "#5D4EFA",
          light: "#7B6FFF",
          dim: "rgba(93,78,250,0.12)",
        },
        ready: "#F0A500",
        "ready-dim": "rgba(240,165,0,0.12)",
        tx: {
          DEFAULT: "#EEF0F6",
          muted: "#6B7A99",
          faint: "#3A445C",
        },
        code: "#0C1020",
      },
      fontFamily: {
        serif: ["Instrument Serif", "Georgia", "serif"],
        sans: ["Inter", "system-ui", "sans-serif"],
        mono: ["JetBrains Mono", "Fira Code", "monospace"],
      },
      backgroundImage: {
        "grid-faint":
          "linear-gradient(rgba(93,78,250,0.04) 1px, transparent 1px), linear-gradient(90deg, rgba(93,78,250,0.04) 1px, transparent 1px)",
        "grid-neutral":
          "linear-gradient(rgba(255,255,255,0.05) 1px, transparent 1px), linear-gradient(90deg, rgba(255,255,255,0.05) 1px, transparent 1px)",
        "accent-radial":
          "radial-gradient(ellipse 60% 40% at 50% 0%, rgba(93,78,250,0.15) 0%, transparent 70%)",
      },
      backgroundSize: {
        grid: "48px 48px",
      },
      animation: {
        "pulse-slow": "pulse 3s cubic-bezier(0.4,0,0.6,1) infinite",
        "fade-up": "fadeUp 0.5s ease forwards",
        "stage-in": "stageIn 0.4s ease forwards",
        crawl: "crawl 2s linear infinite",
        "dot-blink": "dotBlink 1.2s step-end infinite",
      },
      keyframes: {
        fadeUp: {
          from: { opacity: "0", transform: "translateY(12px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        stageIn: {
          from: { opacity: "0", transform: "translateX(-8px)" },
          to: { opacity: "1", transform: "translateX(0)" },
        },
        crawl: {
          from: { transform: "translateX(-100%)" },
          to: { transform: "translateX(300%)" },
        },
        dotBlink: {
          "0%, 100%": { opacity: "1" },
          "50%": { opacity: "0" },
        },
      },
      boxShadow: {
        "accent-glow": "0 0 40px rgba(93,78,250,0.2)",
        "card-hover": "0 8px 32px rgba(0,0,0,0.4), 0 0 0 1px rgba(93,78,250,0.15)",
      },
    },
  },
  plugins: [],
}

export default config
