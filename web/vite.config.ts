import { writeFileSync } from 'node:fs'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// `go:embed all:dist` fails to compile when dist/ is absent, so the directory is
// committed with a placeholder. emptyOutDir wipes it on every build, so put it
// back — otherwise a fresh clone cannot `go build` before `npm run build`.
function keepDistInGit(): Plugin {
  let outDir = ''
  return {
    name: 'keep-dist-in-git',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      writeFileSync(path.join(outDir, '.gitkeep'), '')
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), keepDistInGit()],

  // `npm run dev` serves the SPA on 5173 with HMR and proxies the API to the Go
  // binary on 8080. In production there is no proxy: one binary serves both.
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },

  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
