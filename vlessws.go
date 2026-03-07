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
	"strconv"
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

	// 1. 解析早期数据 (0-RTT)
	// Xray 将早期数据 Base64 编码后放在 Sec-WebSocket-Protocol 头中
	protocol := r.Header.Get("Sec-WebSocket-Protocol")
	var earlyData []byte
	var subprotocols []string // 用于回传给客户端

	if protocol != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(protocol); err == nil {
			earlyData = decoded
			// 【关键修复】必须将该 protocol 加入响应列表，否则客户端会认为握手失败
			subprotocols = []string{protocol}
		}
	}

	// 2. 建立 WebSocket 连接
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols, // 【关键修复】传入子协议
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

	// 3. 根据是否有 Early Data 决定处理逻辑
	if earlyData != nil {
		// 有 0-RTT 数据，直接处理，无需等待第一次 Read
		handleConnection(ctx, conn, earlyData)
	} else {
		// 普通连接，读取第一包数据
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		handleConnection(ctx, conn, msg)
	}
}

func handleConnection(ctx context.Context, ws *websocket.Conn, data []byte) {
	// VLESS 协议头部最小长度校验
	if len(data) < 20 {
		return
	}

	// 解析 VLESS 头部，跳过 UUID 等信息
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

	// 使用 net.JoinHostPort 自动处理 IPv6 的方括号问题
	destAddr := net.JoinHostPort(addr, strconv.Itoa(int(port)))

	// 连接目标服务器
	target, err := dialer.Dial("tcp", destAddr)
	if err != nil {
		return
	}
	defer target.Close()

	// TCP 参数优化
	if tc, ok := target.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	// 将剩余的 Payload (真实请求数据) 写入目标
	// 对于 0-RTT，这里包含了客户端的第一条 HTTP 请求等数据
	if i < len(data) {
		if _, err := target.Write(data[i:]); err != nil {
			return
		}
	}

	// 向客户端发送 VLESS 响应头 (0, 0)
	// 注意：对于 0-RTT，服务端在处理完 Early Data 后必须尽快返回这个响应
	if err := ws.Write(ctx, websocket.MessageBinary, []byte{0, 0}); err != nil {
		return
	}

	// 将 WebSocket 封装为 net.Conn 以便进行 io.Copy
	wsConn := websocket.NetConn(ctx, ws, websocket.MessageBinary)

	// 双向数据转发
	var wg sync.WaitGroup
	wg.Add(2)

	// target -> websocket
	go func() {
		defer wg.Done()
		io.Copy(wsConn, target)
		wsConn.Close() // 目标关闭后，关闭 WS
	}()

	// websocket -> target
	go func() {
		defer wg.Done()
		io.Copy(target, wsConn)
		target.Close() // WS 关闭后，关闭目标连接
	}()

	wg.Wait()
}
