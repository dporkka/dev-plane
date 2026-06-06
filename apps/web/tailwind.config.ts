import type { Config } from 'tailwindcss';

const config: Config = {
  darkMode: 'class',
  content: [
    './app/**/*.{js,ts,jsx,tsx,mdx}',
    './components/**/*.{js,ts,jsx,tsx,mdx}',
    './lib/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        border: '#30363d',
        background: '#0d1117',
        foreground: '#c9d1d9',
        card: {
          DEFAULT: '#161b22',
          foreground: '#c9d1d9',
        },
        primary: {
          DEFAULT: '#3b82f6',
          foreground: '#ffffff',
          hover: '#2563eb',
        },
        secondary: {
          DEFAULT: '#1f2937',
          foreground: '#e5e7eb',
        },
        muted: {
          DEFAULT: '#21262d',
          foreground: '#8b949e',
        },
        accent: {
          DEFAULT: '#388bfd',
          foreground: '#ffffff',
        },
        destructive: {
          DEFAULT: '#ef4444',
          foreground: '#ffffff',
        },
        ring: '#3b82f6',
      },
      borderRadius: {
        lg: '0.5rem',
        md: '0.375rem',
        sm: '0.25rem',
      },
      fontFamily: {
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
};

export default config;
