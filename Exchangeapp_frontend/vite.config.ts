// https://vitejs.dev/config/
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  server: { 
    host: '0.0.0.0', 
    proxy: {
      '/api': {
        target:  'http://127.0.0.1:3000',
        changeOrigin: true,
        secure: false,
        //rewrite: (path) => path.replace(/^\/api/, '')
      }
    }
  }
});
