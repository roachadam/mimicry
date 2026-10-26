package server

import (
	"slices"
	"time"
)

// wsH1Host is the proxy websocket pages' second host: same-site with
// wsHost, but its HTTP/2 connections do not advertise extended CONNECT,
// so a socket to it goes on an HTTP/1.1 connection of its own.
const wsH1Host = "h1.ws-a.test"

// ProxyWebSocket, ProxyWebSocketAuth and ProxyWebSocketSOCKS5 are the
// proxy websocket scenarios: a websocket page (ProxyWebSocketSteps)
// through the capture proxy as the other proxy scenarios configure it,
// with an HTTP proxy, one demanding Basic credentials, and SOCKS5. They
// record what goes to the proxy for each socket (a CONNECT or SOCKS5
// request, or the handshake sent whole), which tunnel carries it
// (capture.WebSocket.ProxyConn) and the handshake inside.
var (
	ProxyWebSocket = &Scenario{
		Name:         "proxy-websocket",
		Doc:          "WebSockets through an HTTP proxy: a wss:// socket where the page's HTTP/2 connection allows extended CONNECT, one where it does not, and ws:// from an http:// page; what each sends the proxy (CONNECT, or the handshake in absolute form) and which tunnel carries it",
		Proxy:        ProxyHTTP,
		connectHosts: []string{wsHost},
		multi:        proxyWSPage(true),
		plainPort:    true,
	}
	ProxyWebSocketAuth = &Scenario{
		Name:         "proxy-websocket-auth",
		Doc:          "the proxy-websocket page through an HTTP proxy demanding Basic credentials: whether each socket's CONNECT (or forwarded handshake) is challenged or sends credentials unasked",
		Proxy:        ProxyHTTP,
		ProxyAuth:    true,
		connectHosts: []string{wsHost},
		multi:        proxyWSPage(true),
		plainPort:    true,
	}
	ProxyWebSocketSOCKS5 = &Scenario{
		Name:         "proxy-websocket-socks5",
		Doc:          "the proxy-websocket page's wss:// sockets through a SOCKS5 proxy (no ws://: Safari's proxy script sends only one port to SOCKS5)",
		Proxy:        ProxySOCKS5,
		connectHosts: []string{wsHost},
		multi:        proxyWSPage(false),
	}
)

// proxyWSPage is the proxy websocket page, with its http:// page and ws://
// socket when plain.
func proxyWSPage(plain bool) wsPage {
	sockets, steps := ProxyWebSocketSteps(plain)
	return wsPage{
		fixedHosts: fixedHosts{
			names: []string{
				wsHost,
				wsH1Host,
			},
			runFor: 60 * time.Second,
		},
		sockets: sockets,
		steps:   steps,
	}
}

// ProxyWebSocketSteps lists the proxy websocket page's sockets and page
// steps: the page on wsHost (whose HTTP/2 connections allow extended
// CONNECT) fetches from wsH1Host (whose do not), so each host has an
// HTTP/2 connection, as on the websocket pages; then it opens px-h2 on
// wsHost and px-h1 and px-h1-again on wsH1Host (whether the second
// socket's TLS connection resumes the first's session); with plain, it
// goes to its http:// page, which opens px-plain (ws://), and back. Each
// socket sends text of 5 and 200 bytes and closes with 1000 "bye".
func ProxyWebSocketSteps(plain bool) ([]WebSocketStep, []WebSocketPageStep) {
	sends := []WSMessage{
		{
			Size: 5,
		},
		{
			Size: 200,
		},
	}
	sockets := []WebSocketStep{
		{
			Label: "px-h2",
			Page:  "doc",
			Host:  wsHost,
			Sends: sends,
			Close: true,
		},
		{
			Label: "px-h1",
			Page:  "doc",
			Host:  wsH1Host,
			Sends: sends,
			Close: true,
		},
		{
			Label: "px-h1-again",
			Page:  "doc",
			Host:  wsH1Host,
			Sends: sends,
			Close: true,
		},
	}
	steps := []WebSocketPageStep{
		{
			Label: "doc",
			Host:  wsHost,
			Path:  "/",
			Nav:   true,
		},
		{
			Label: "h1-warm",
			Host:  wsH1Host,
			Path:  "/px/warm",
		},
	}
	if !plain {
		return sockets, steps
	}
	return slices.Concat(sockets, []WebSocketStep{
			{
				Label: "px-plain",
				Page:  "plain",
				Host:  wsHost,
				Plain: true,
				Sends: sends,
				Close: true,
			},
		}), slices.Concat(steps, []WebSocketPageStep{
			{
				Label: "plain",
				Host:  wsHost,
				Plain: true,
				Path:  "/px/plain",
				Nav:   true,
			},
			{
				Label: "end",
				Host:  wsHost,
				Path:  "/px/end",
				Nav:   true,
			},
		})
}
