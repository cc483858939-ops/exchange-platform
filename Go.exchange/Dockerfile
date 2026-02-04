FROM mcr.microsoft.com/devcontainers/go:1-1.21-bullseye

# 1. ❌ 千万不要换源！保留官方源，配合你的代理最稳定
# (此处保持原样)

# 2. 修复 GPG 错误并安装 Docker
# 核心改动：在第一行增加了 rm -f /etc/apt/sources.list.d/yarn.list
RUN rm -f /etc/apt/sources.list.d/yarn.list && \
    apt-get update --fix-missing && \
    apt-get install -y docker.io && \
    usermod -aG docker vscode

# 3. 安装 Go 1.25.2
RUN wget https://golang.google.cn/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tar.gz \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
USER vscode