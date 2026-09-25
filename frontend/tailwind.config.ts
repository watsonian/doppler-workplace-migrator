import type { Config } from 'tailwindcss';

/**
 * Colours, type and radii come from the Doppler Product UI Design System
 * (`.my/Product UI Design System/colors_and_type.css`), not from approximations.
 * The migrator is meant to read as part of the dashboard, and a palette that is
 * nearly right is what makes a tool feel like a clone of the product rather than
 * part of it.
 */
const config: Config = {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        gray: {
          0: '#FFFFFF',
          25: '#FAFAFA',
          50: '#F4F4F6',
          75: '#E9EBED',
          100: '#D2D5DA',
          200: '#B9BDC6',
          300: '#A8ADB7',
          400: '#8E95A2',
          500: '#6B7280',
          600: '#5B616E',
          700: '#4A4E5A',
          800: '#3D4048',
          900: '#25272C',
          950: '#1C1D21',
          975: '#131416',
        },
        purple: {
          50: '#F9F5FF',
          100: '#E9DDFE',
          200: '#C8A7FB',
          300: '#A976F9',
          400: '#8B45F7',
          500: '#6B13F5',
          600: '#570FC7',
          700: '#440C9D',
        },
        // Focus is blue, never purple: purple means "selected" everywhere else in
        // the product, and a focus ring that borrows it reads as a selection.
        blue: { 50: '#F0F6FF', 100: '#E5F0FF', 500: '#0065FF', 600: '#0052CC' },
        red: { 50: '#FEF1F0', 100: '#FDDAD8', 500: '#F52215', 600: '#C81A10' },
        green: { 50: '#EBFBF3', 100: '#D2F4E3', 500: '#12B76A', 600: '#0E9455' },
        yellow: { 50: '#FEF8EB', 100: '#FDEBC9', 500: '#F5A612', 600: '#C48310' },

        /*
         * Legacy semantic names, re-pointed at the design system's values.
         *
         * These predate the system palette above and are still used across the
         * pages. Aliasing rather than sweeping means every screen picks up the
         * correct colour immediately, and a page can move to the real names as
         * it is rewritten rather than in one risky pass. Nothing new should use
         * them.
         */
        primary: {
          50: '#F9F5FF',
          100: '#E9DDFE',
          200: '#C8A7FB',
          400: '#8B45F7',
          500: '#8B45F7',
          600: '#6B13F5',
          700: '#570FC7',
          800: '#440C9D',
          900: '#2E0A5C',
        },
        success: { 50: '#EBFBF3', 100: '#D2F4E3', 500: '#12B76A', 600: '#0E9455', 700: '#0B7644' },
        error: { 50: '#FEF1F0', 100: '#FDDAD8', 500: '#F52215', 600: '#C81A10', 700: '#A2150D' },
        warning: { 50: '#FEF8EB', 100: '#FDEBC9', 500: '#F5A612', 600: '#C48310' },
        info: { 50: '#F0F6FF', 100: '#E5F0FF', 500: '#0065FF', 600: '#0052CC' },
      },
      fontFamily: {
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'sans-serif'],
        // Roboto Mono is the system's mono face. Reserved for identifiers —
        // secret names, config paths, tokens, URLs — never for running text.
        mono: ['Roboto Mono', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      borderRadius: {
        DEFAULT: '8px',
        sm: '6px',
        md: '8px',
        lg: '10px',
      },
      boxShadow: {
        100: '0 1px 2px rgba(19, 20, 22, 0.05)',
        200: '0 2px 6px rgba(19, 20, 22, 0.08)',
        400: '0 10px 34px rgba(19, 20, 22, 0.18)',
      },
      transitionDuration: {
        // The system's interaction timing.
        DEFAULT: '100ms',
      },
      backgroundImage: {
        'brand-gradient': 'linear-gradient(90deg, #8B45F7 0%, #570FC7 62.15%)',
      },
      keyframes: {
        'fade-in': {
          from: { opacity: '0' },
          to: { opacity: '1' },
        },
      },
      animation: {
        'fade-in': 'fade-in 120ms ease-out',
        'spin-slow': 'spin 0.9s linear infinite',
      },
    },
  },
  plugins: [],
};

export default config;
