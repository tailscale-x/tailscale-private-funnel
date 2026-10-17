export default {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        funnel: {
          ink: "#10213f",
          muted: "#61708d",
          canvas: "#f4f7fb",
          line: "#dce4f0",
          blue: "#2563eb",
          green: "#15945a",
          amber: "#c98309",
          red: "#d64141"
        }
      },
      boxShadow: {
        panel: "0 12px 32px rgba(16, 33, 63, 0.06)"
      }
    }
  },
  plugins: [require("daisyui")],
  daisyui: {
    themes: [{
      funnel: {
        "primary": "#2563eb",
        "primary-content": "#ffffff",
        "secondary": "#61708d",
        "accent": "#15945a",
        "neutral": "#10213f",
        "base-100": "#ffffff",
        "base-200": "#f4f7fb",
        "base-300": "#dce4f0",
        "base-content": "#10213f",
        "info": "#2563eb",
        "success": "#15945a",
        "warning": "#c98309",
        "error": "#d64141",
        "border-radius-selector": "0.5rem",
        "border-radius-field": "0.5rem",
        "border-radius-box": "0.75rem"
      }
    }]
  }
};
