#!/bin/sh

# ==========================================
# 环境变量配置
# ==========================================
VERSION="v2.0"
INSTALL_PATH="/usr/local/bin/vlessws"

# ==========================================
# 检查 root 权限
# ==========================================
if [ "$(id -u)" != "0" ]; then
    echo "错误: 请使用 root 用户运行此脚本。"
    exit 1
fi

# ==========================================
# 安装功能模块
# ==========================================
do_install() {
    echo "请确保 80 端口未被其他程序占用..."

    # 1. 获取系统架构和最佳版本
    ARCH=$(uname -m)
    if [ "$ARCH" = "x86_64" ]; then
        if grep -q "avx512f" /proc/cpuinfo; then
            echo "检测到 CPU 支持 AVX-512，使用最高性能 v4 版本。"
            FILE_NAME="VlessWS-linux-amd64-v4"
        elif grep -q "avx2" /proc/cpuinfo; then
            echo "检测到 CPU 支持 AVX2，使用高性能 v3 版本。"
            FILE_NAME="VlessWS-linux-amd64-v3"
        elif grep -q "popcnt" /proc/cpuinfo; then
            echo "检测到 CPU 支持 SSE4/POPCNT，使用 v2 版本。"
            FILE_NAME="VlessWS-linux-amd64-v2"
        else
            echo "未检测到高级指令集，使用基础兼容 v1 版本。"
            FILE_NAME="VlessWS-linux-amd64-v1"
        fi
    elif [ "$ARCH" = "aarch64" ]; then
        echo "检测到 ARM64 架构，使用 arm-v8.2 版本。"
        FILE_NAME="VlessWS-arm64-v8.2"
    else
        echo "错误: 不支持的系统架构 $ARCH"
        exit 1
    fi

    DOWNLOAD_URL="https://github.com/kirito201711/vlessws/releases/download/${VERSION}/${FILE_NAME}"

    echo "================================================="
    echo "准备下载: $FILE_NAME"
    echo "================================================="

    # 2. 下载并赋予执行权限
    if command -v wget >/dev/null 2>&1; then
        wget -qO "$INSTALL_PATH" "$DOWNLOAD_URL"
    elif command -v curl >/dev/null 2>&1; then
        curl -sL "$DOWNLOAD_URL" -o "$INSTALL_PATH"
    else
        echo "错误: 系统中未找到 wget 或 curl。"
        exit 1
    fi

    if [ ! -s "$INSTALL_PATH" ]; then
        echo "错误: 下载失败，请检查网络或 GitHub 连通性。"
        exit 1
    fi

    chmod +x "$INSTALL_PATH"
    echo "下载完成并已赋予执行权限。"

    # 3. 配置开机自启
    if command -v systemctl >/dev/null 2>&1; then
        echo "检测到 Systemd，正在配置服务..."
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
        systemctl enable vlessws >/dev/null 2>&1
        systemctl restart vlessws
        
        echo "================================================="
        echo "安装成功！VlessWS 已启动 (Systemd)。"
        echo "查看状态: systemctl status vlessws"
        echo "================================================="

    elif command -v rc-update >/dev/null 2>&1; then
        echo "检测到 OpenRC，正在配置服务..."
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
        rc-update add vlessws default >/dev/null 2>&1
        rc-service vlessws restart
        
        echo "================================================="
        echo "安装成功！VlessWS 已启动 (OpenRC)。"
        echo "查看状态: rc-service vlessws status"
        echo "================================================="
    else
        echo "警告: 未知初始化系统，已下载至 $INSTALL_PATH，需手动运行。"
    fi
}

# ==========================================
# 卸载功能模块
# ==========================================
do_uninstall() {
    echo "正在准备卸载 VlessWS..."

    # 1. 停止并删除 Systemd 服务
    if command -v systemctl >/dev/null 2>&1; then
        if [ -f "/etc/systemd/system/vlessws.service" ]; then
            systemctl stop vlessws >/dev/null 2>&1
            systemctl disable vlessws >/dev/null 2>&1
            rm -f /etc/systemd/system/vlessws.service
            systemctl daemon-reload
            echo "已清理 Systemd 服务配置。"
        fi
    fi

    # 2. 停止并删除 OpenRC 服务
    if command -v rc-update >/dev/null 2>&1; then
        if [ -f "/etc/init.d/vlessws" ]; then
            rc-service vlessws stop >/dev/null 2>&1
            rc-update del vlessws default >/dev/null 2>&1
            rm -f /etc/init.d/vlessws
            echo "已清理 OpenRC 服务配置。"
        fi
    fi

    # 3. 删除二进制文件和日志
    rm -f "$INSTALL_PATH"
    rm -f /var/log/vlessws.log
    rm -f /var/log/vlessws.err
    
    echo "================================================="
    echo "卸载成功！VlessWS 及其服务配置已被完全清除。"
    echo "================================================="
}

# ==========================================
# 交互菜单
# ==========================================
clear
echo "================================================="
echo "  VlessWS 一键安装与管理脚本"
echo "================================================="
echo "  1. 安装 VlessWS"
echo "  2. 卸载 VlessWS"
echo "  0. 退出脚本"
echo "================================================="
printf "请输入数字选择 [0-2]: "

# 兼容 curl 直接管道执行时的输入读取
read choice </dev/tty

case "$choice" in
    1)
        do_install
        ;;
    2)
        do_uninstall
        ;;
    0)
        echo "已退出。"
        exit 0
        ;;
    *)
        echo "错误: 输入无效，请输入 0、1 或 2。"
        exit 1
        ;;
esac
