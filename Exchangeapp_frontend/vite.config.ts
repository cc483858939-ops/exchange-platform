// https://vitejs.dev/config/
import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  server: {
    host: '0.0.0.0',
    port: 5173,
    // HMR 配置：容器内 HMR 需要指定对外暴露的主机，否则 WebSocket 连接失败
    hmr: {
      clientPort: 5173,
    },
    proxy: {
      '/api': {
        // 通过环境变量切换：
        //   容器环境：docker-compose 中设置 API_URL=http://api:3000
        //   本地开发：不设置环境变量，自动回退到 http://127.0.0.1:3000
        target: process.env.API_URL || 'http://127.0.0.1:3000',
        changeOrigin: true,
        secure: false,
        //rewrite: (path) => path.replace(/^\/api/, '')
      }
    }
  }
});
