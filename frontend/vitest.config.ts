import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Unit tests do not need Wails' native bridge or its Vite plugin.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    clearMocks: true,
  },
})
