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
	"github.com/coder/websocket"
)

// 全局 Dialer
var dialer = &net.Dialer{
	Timeout:   10 * time.Second,
	KeepAlive: 30 * time.Second,
}

// 1. 定义全局内存池，用于 io.CopyBuffer 复用内存，消除 GC 压力
var bufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return &buf
	},
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
	protocol := r.Header.Get("Sec-WebSocket-Protocol")
	var earlyData []byte
	var subprotocols []string

	if protocol != "" {
		if decoded, err := base64.RawURLEncoding.DecodeString(protocol); err == nil {
			earlyData = decoded
			subprotocols = []string{protocol}
		}
	}

	// 2. 建立 WebSocket 连接
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       subprotocols,
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	conn.SetReadLimit(-1)

	ctx := context.Background()

	// 3. 根据是否有 Early Data 决定处理逻辑
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
	// VLESS 协议头部最小长度校验 (原始逻辑，不校验UUID)
	if len(data) < 20 {
		return
	}

	// 解析 VLESS 头部，跳过 UUID 等信息 (原始逻辑)
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

	destAddr := net.JoinHostPort(addr, strconv.Itoa(int(port)))

	// 连接目标服务器
	target, err := dialer.Dial("tcp", destAddr)
	if err != nil {
		return
	}
	defer target.Close()

	if tc, ok := target.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	// 将剩余的 Payload (真实请求数据) 写入目标
	if i < len(data) {
		if _, err := target.Write(data[i:]); err != nil {
			return
		}
	}

	// 向客户端发送 VLESS 响应头 (0, 0)
	if err := ws.Write(ctx, websocket.MessageBinary, []byte{0, 0}); err != nil {
		return
	}

	wsConn := websocket.NetConn(ctx, ws, websocket.MessageBinary)

	var wg sync.WaitGroup
	wg.Add(2)

	// --- 核心优化点：使用 sync.Pool 和 io.CopyBuffer ---

	// target -> websocket
	go func() {
		defer wg.Done()
		
		// 从池中获取 buffer
		bufPtr := bufPool.Get().(*[]byte)
		defer bufPool.Put(bufPtr) // 用完归还

		// 使用 CopyBuffer，0 分配转发
		io.CopyBuffer(wsConn, target, *bufPtr)
		wsConn.Close() // 维持原始关闭逻辑
	}()

	// websocket -> target
	go func() {
		defer wg.Done()

		// 从池中获取 buffer
		bufPtr := bufPool.Get().(*[]byte)
		defer bufPool.Put(bufPtr) // 用完归还
		
		// 使用 CopyBuffer，0 分配转发
		io.CopyBuffer(target, wsConn, *bufPtr)
		target.Close() // 维持原始关闭逻辑
	}()

	wg.Wait()
}
