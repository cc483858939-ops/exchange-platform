# 使用官方维护的开发基础镜像（它其实已经内置了很多工具）
FROM mcr.microsoft.com/devcontainers/go:1.24-bookworm

# 切换到 root 进行系统级配置
USER root

# 1. 精简安装：只保留开发必须的工具，删掉 ssh
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    sudo \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

# 2. 权限配置：确保 vscode 用户有 sudo 权限（无需密码）

RUN echo "vscode ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# 3. 安装指定版本的 Go 

RUN wget https://golang.google.cn/dl/go1.24.0.linux-amd64.tar.gz -O /tmp/go.tar.gz \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

# 设置环境变量
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/home/vscode/go"
ENV PATH="${GOPATH}/bin:${PATH}"

# 4. 切换回开发用户（这才是真实开发的常态，不推荐一直用 root）
USER vscode
WORKDIR /workspace
# 5. 保持容器运行（不启动 SSH，只启动一个 shell）
CMD ["sleep", "infinity"]