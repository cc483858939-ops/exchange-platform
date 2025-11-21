# 1. 仍然从我们已知的、可以正常工作的 Go 1.21 镜像开始
FROM mcr.microsoft.com/devcontainers/go:1-1.21-bullseye

# 2. 切换到 root 用户，以便有权限安装软件
USER root

# 3. 更换 Debian 的软件包源为国内镜像，确保 apt-get 速度
RUN sed -i 's/deb.debian.org/mirrors.aliyun.com/g' /etc/apt/sources.list

# 4. 安装必要的工具，比如 wget 用于下载
RUN apt-get update && export DEBIAN_FRONTEND=noninteractive \
    && apt-get -y install --no-install-recommends wget default-mysql-client \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# 5. 【核心步骤】手动下载并安装 Go 1.25.2
#    我们从 Go 官方的国内镜像站 golang.google.cn 下载，速度飞快
RUN wget https://golang.google.cn/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tar.gz \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

# 6. 将新的 Go 路径添加到环境变量中 (这一步确保系统能找到新的 Go)
ENV PATH="/usr/local/go/bin:${PATH}"

# 7. 切换回默认的非 root 用户 'vscode'，更安全
USER vscode