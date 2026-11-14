/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the console talks to a running demo stack through this proxy, so the browser
// sees one origin, exactly as when the control API serves the built console.
const target = process.env.GJ_CONTROL_URL ?? 'http://localhost:18788';

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/v1': { target, changeOrigin: true },
      '/demo': { target, changeOrigin: true },
    },
  },
  build: {
    target: 'es2022',
    sourcemap: true,
    chunkSizeWarningLimit: 900,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
