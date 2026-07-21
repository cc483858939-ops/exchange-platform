# ---- 前端开发容器 ----
# 使用轻量 Alpine 镜像，支持 Vite Dev Server + HMR 热重载
FROM node:20-alpine

# 设置工作目录
WORKDIR /app

# 优先复制依赖文件，利用 Docker 层缓存
# 只有 package.json 变化时才重新 npm ci，src 变化不会触发重装
COPY package.json package-lock.json ./

# 安装依赖（ci 模式：严格按 lock 文件，适合 CI/容器环境）
RUN npm ci

# 复制项目源码（node_modules 已通过 .dockerignore 排除）
COPY . .

# 暴露 Vite 默认端口
EXPOSE 5173

# 启动 Vite 开发服务器
# host 已在 vite.config.ts 中配置为 0.0.0.0，无需在此重复指定
CMD ["npm", "run", "dev"]
