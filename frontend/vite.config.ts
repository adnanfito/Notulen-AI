import { fileURLToPath, URL } from 'node:url'
// vitest/config re-exports Vite's defineConfig with the `test` block typed.
import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// BACKEND_ORIGIN differs between running in Docker (service name) and on the
// host (localhost), so it comes from the environment rather than a constant.
const backendOrigin = process.env.BACKEND_ORIGIN ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
  },
  server: {
    host: true,
    port: 5173,
    // Proxying keeps the browser on one origin, so dev never hits CORS.
    proxy: {
      '/api': {
        target: backendOrigin,
        changeOrigin: true,
      },
    },
  },
})
