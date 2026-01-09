FROM mcr.microsoft.com/devcontainers/go:1-1.21-bullseye

# 1. ❌ 千万不要换源！保留官方源，配合你的代理最稳定
# RUN sed -i ... (不要写这一行)

# 2. 手动安装 Docker (回归最原始的方法，但在前面加了 fix-missing 防止断连)
# 注意：这里加了 usermod 把 vscode 加入 docker 组，配合 postCreateCommand 双保险
RUN apt-get update --fix-missing && apt-get install -y docker.io \
    && usermod -aG docker vscode

# 3. 安装 Go 1.25.2
RUN wget https://golang.google.cn/dl/go1.25.2.linux-amd64.tar.gz -O /tmp/go.tar.gz \
    && rm -rf /usr/local/go \
    && tar -C /usr/local -xzf /tmp/go.tar.gz \
    && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
USER vscode