package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv" // 新增引入，用于转换端口号
	"sync"
	"time"

	"nhooyr.io/websocket"
)

// 全局 Dialer
var dialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

func main() {
	server := &http.Server{
		Addr:              ":80",
		Handler:           http.HandlerFunc(handleWS),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Println("Server listening on :80")
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "websocket" {
		return
	}

	// 解析早期数据
	protocol := r.Header.Get("Sec-WebSocket-Protocol")
	var earlyData []byte
	if protocol != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(protocol); err == nil {
			earlyData = decoded
		}
	}

	// nhooyr.io/websocket
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// 取消读取限制，防止大包传输中断
	conn.SetReadLimit(-1)

	ctx := context.Background()

	if earlyData != nil {
		handleConnection(ctx, conn, earlyData)
	} else {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		handleConnection(ctx, conn, msg)
	}
}

func handleConnection(ctx context.Context, ws *websocket.Conn, data []byte) {
	// 解析协议头
	if len(data) < 20 {
		return
	}

	// 跳过 UUID (16字节) + 版本等信息，定位到目标地址部分
	// VLESS 协议头部结构依赖这里，保持原逻辑不变
	i := 19 + int(data[17])
	if i+3 > len(data) {
		return
	}

	port := binary.BigEndian.Uint16(data[i : i+2])
	addrType := data[i+2]
	i += 3

	var addr string
	switch addrType {
	case 1: // IPv4
		if i+4 > len(data) {
			return
		}
		ip := netip.AddrFrom4([4]byte{data[i], data[i+1], data[i+2], data[i+3]})
		addr = ip.String()
		i += 4
	case 2: // 域名
		if i+1 > len(data) {
			return
		}
		domainLen := int(data[i])
		i++
		if i+domainLen > len(data) {
			return
		}
		addr = string(data[i : i+domainLen])
		i += domainLen
	case 3: // IPv6
		if i+16 > len(data) {
			return
		}
		var ipBytes [16]byte
		copy(ipBytes[:], data[i:i+16])
		ip := netip.AddrFrom16(ipBytes)
		addr = ip.String()
		i += 16
	default:
		return
	}

	// ---------------------------------------------------------
	// 修复核心：使用 net.JoinHostPort 处理 IPv6 格式问题
	// ---------------------------------------------------------
	destAddr := net.JoinHostPort(addr, strconv.Itoa(int(port)))

	// 连接目标
	target, err := dialer.Dial("tcp", destAddr)
	if err != nil {
		// 可以在这里加日志查看连接失败原因
		// fmt.Printf("Connect to %s failed: %v\n", destAddr, err)
		return
	}
	defer target.Close()

	// TCP 优化
	if tc, ok := target.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	// 发送剩余数据 (Payload)
	if i < len(data) {
		if _, err := target.Write(data[i:]); err != nil {
			return
		}
	}

	// 发送响应 (VLESS 响应头)
	if err := ws.Write(ctx, websocket.MessageBinary, []byte{0, 0}); err != nil {
		return
	}

	// 使用 NetConn 将 WebSocket 转为 net.Conn
	wsConn := websocket.NetConn(ctx, ws, websocket.MessageBinary)

	// 双向 io.Copy
	var wg sync.WaitGroup
	wg.Add(2)

	// target -> websocket
	go func() {
		defer wg.Done()
		io.Copy(wsConn, target)
		wsConn.Close()
	}()

	// websocket -> target
	go func() {
		defer wg.Done()
		io.Copy(target, wsConn)
		target.Close()
	}()

	wg.Wait()
}
