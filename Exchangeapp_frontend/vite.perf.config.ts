import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

const workspaceRoot = fileURLToPath(new URL('.', import.meta.url));
const perfRoot = fileURLToPath(new URL('./perf/', import.meta.url));
const perfOutput = fileURLToPath(new URL('./dist-perf/', import.meta.url));

const readGitHead = (): string => {
  try {
    return execFileSync('git', ['rev-parse', 'HEAD'], {
      cwd: workspaceRoot,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    }).trim() || 'unknown';
  } catch {
    return 'unknown';
  }
};

export default defineConfig({
  root: perfRoot,
  plugins: [vue()],
  define: {
    __EXCHANGE_PERF_GIT_HEAD__: JSON.stringify(readGitHead()),
  },
  server: {
    host: '0.0.0.0',
    port: 5174,
    fs: {
      allow: [workspaceRoot],
    },
  },
  preview: {
    host: '0.0.0.0',
    port: 4174,
  },
  build: {
    outDir: perfOutput,
    emptyOutDir: true,
  },
});
