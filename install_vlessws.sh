cat << 'EOF' > install_vlessws.sh
#!/bin/sh

# 检查是否为 root 用户
if [ "$(id -u)" != "0" ]; then
    echo "错误: 请使用 root 用户运行此脚本。"
    exit 1
fi

echo "请确保 80 端口未被其他程序占用..."

# 获取系统架构
ARCH=$(uname -m)

if [ "$ARCH" = "x86_64" ]; then
    # 自动根据 CPU 指令集选择最佳版本 (v4 -> v3 -> v2 -> v1)
    if grep -q "avx512f" /proc/cpuinfo; then
        echo "检测到 CPU 支持 AVX-512 指令集，将使用最高性能的 v4 版本。"
        FILE_NAME="VlessWS-linux-amd64-v4"
    elif grep -q "avx2" /proc/cpuinfo; then
        echo "检测到 CPU 支持 AVX2 指令集，将使用高性能的 v3 版本。"
        FILE_NAME="VlessWS-linux-amd64-v3"
    elif grep -q "popcnt" /proc/cpuinfo; then
        echo "检测到 CPU 支持 SSE4/POPCNT 指令集，将使用 v2 版本。"
        FILE_NAME="VlessWS-linux-amd64-v2"
    else
        echo "未检测到高级指令集，将使用最兼容的 v1 基础版本。"
        FILE_NAME="VlessWS-linux-amd64-v1"
    fi
elif [ "$ARCH" = "aarch64" ]; then
    echo "检测到 ARM64 架构，将使用 arm-v8.2 版本。"
    FILE_NAME="VlessWS-arm64-v8.2"
else
    echo "错误: 不支持的系统架构 $ARCH"
    exit 1
fi

VERSION="v2.0"
DOWNLOAD_URL="https://github.com/kirito201711/vlessws/releases/download/${VERSION}/${FILE_NAME}"
INSTALL_PATH="/usr/local/bin/vlessws"

echo "================================================="
echo "准备下载文件: $FILE_NAME"
echo "================================================="

# 下载文件
if command -v wget >/dev/null 2>&1; then
    wget -qO "$INSTALL_PATH" "$DOWNLOAD_URL"
elif command -v curl >/dev/null 2>&1; then
    curl -sL "$DOWNLOAD_URL" -o "$INSTALL_PATH"
else
    echo "错误: 系统中未找到 wget 或 curl，请先安装它们。"
    exit 1
fi

# 检查是否下载成功
if [ ! -s "$INSTALL_PATH" ]; then
    echo "错误: 下载失败，请检查网络或 GitHub 的连通性。"
    exit 1
fi

# 赋予执行权限
chmod +x "$INSTALL_PATH"
echo "二进制文件已安装至 $INSTALL_PATH 并赋予执行权限。"

# ==========================================
# 配置开机自启和服务
# ==========================================

# 1. 适配 Debian / Ubuntu 等使用 Systemd 的系统
if command -v systemctl >/dev/null 2>&1; then
    echo "检测到 Systemd (Debian/Ubuntu 环境)，正在配置服务..."
    
    cat << 'SERVICE_EOF' > /etc/systemd/system/vlessws.service
[Unit]
Description=VlessWS Service
After=network.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/vlessws
Restart=on-failure
RestartSec=5s
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SERVICE_EOF

    systemctl daemon-reload
    systemctl enable vlessws
    systemctl restart vlessws
    
    echo "================================================="
    echo "安装成功！VlessWS 已经启动并设置为开机自启 (Systemd)。"
    echo "查看运行状态: systemctl status vlessws"
    echo "查看运行日志: journalctl -u vlessws -f"
    echo "================================================="

# 2. 适配 Alpine 等使用 OpenRC 的系统
elif command -v rc-update >/dev/null 2>&1; then
    echo "检测到 OpenRC (Alpine 环境)，正在配置服务..."
    
    cat << 'INIT_EOF' > /etc/init.d/vlessws
#!/sbin/openrc-run

name="vlessws"
description="VlessWS Service"
command="/usr/local/bin/vlessws"
command_background="yes"
pidfile="/run/${name}.pid"
output_log="/var/log/${name}.log"
error_log="/var/log/${name}.err"

depend() {
    need net
}
INIT_EOF

    chmod +x /etc/init.d/vlessws
    rc-update add vlessws default
    rc-service vlessws restart
    
    echo "================================================="
    echo "安装成功！VlessWS 已经启动并设置为开机自启 (OpenRC)。"
    echo "查看运行状态: rc-service vlessws status"
    echo "查看运行日志: tail -f /var/log/vlessws.log"
    echo "================================================="

else
    echo "警告: 未知或不支持的初始化系统。"
    echo "程序已下载到 $INSTALL_PATH，你需要手动运行。"
    exit 1
fi
EOF

sh install_vlessws.sh
