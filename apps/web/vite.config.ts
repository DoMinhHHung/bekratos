import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const authProxyTarget =
  (typeof process !== 'undefined' && process.env?.VITE_AUTH_URL) ||
  'https://p01--pizza--4gxsm5p6nnbw.code.run'

export default defineConfig({
  plugins: [react()],
  server: {
    port: 3000,
    allowedHosts: true,
    proxy: {
      '/api': {
        target: authProxyTarget,
        changeOrigin: true,
        secure: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    css: false,
  },
})
