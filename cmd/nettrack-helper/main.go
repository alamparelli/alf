//go:build linux

// nettrack-helper is a privileged helper that subscribes to conntrack NEW events
// and writes them as JSON lines to a Unix socket. It runs as root with CAP_NET_ADMIN
// before the daemon drops capabilities via setpriv.
package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	ct "github.com/ti-mo/conntrack"
	"github.com/ti-mo/netfilter"
)

// ConnEvent is a compact representation of a new outbound connection.
type ConnEvent struct {
	Proto uint8  `json:"proto"` // 6=TCP, 17=UDP
	SrcIP string `json:"src"`
	DstIP string `json:"dst"`
	DPort uint16 `json:"dport"`
	TS    int64  `json:"ts"`
}

const sockPath = "/run/alf-nettrack.sock"

func main() {
	log.SetPrefix("[nettrack] ")
	log.SetFlags(log.Ltime)

	// Clean up stale socket.
	os.Remove(sockPath)

	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatalf("listen %s: %v", sockPath, err)
	}
	defer ln.Close()

	// Allow alf user (uid 1000) to connect.
	os.Chmod(sockPath, 0o666)

	log.Printf("listening on %s", sockPath)

	// Open conntrack netlink connection.
	c, err := ct.Dial(nil)
	if err != nil {
		log.Fatalf("conntrack dial: %v", err)
	}
	defer c.Close()

	evCh := make(chan ct.Event, 256)
	errCh, err := c.Listen(evCh, 1, []netfilter.NetlinkGroup{netfilter.GroupCTNew})
	if err != nil {
		log.Fatalf("conntrack listen: %v", err)
	}

	log.Println("subscribed to conntrack NEW events")

	// Track connected clients.
	type client struct {
		conn net.Conn
		enc  *json.Encoder
	}
	clients := make(map[net.Conn]*client)
	newClients := make(chan net.Conn, 4)

	// Accept connections in background.
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			newClients <- conn
		}
	}()

	// Graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case ev := <-evCh:
			if ev.Type != ct.EventNew {
				continue
			}
			flow := ev.Flow

			// Only TCP (6) and UDP (17).
			proto := flow.TupleOrig.Proto.Protocol
			if proto != 6 && proto != 17 {
				continue
			}

			dst := flow.TupleOrig.IP.DestinationAddress
			if !dst.IsValid() {
				continue
			}

			// Skip loopback and link-local.
			if dst.IsLoopback() || dst.IsLinkLocalUnicast() {
				continue
			}

			ce := ConnEvent{
				Proto: proto,
				SrcIP: flow.TupleOrig.IP.SourceAddress.String(),
				DstIP: dst.String(),
				DPort: flow.TupleOrig.Proto.DestinationPort,
				TS:    time.Now().Unix(),
			}

			// Broadcast to all connected clients.
			for conn, cl := range clients {
				if err := cl.enc.Encode(ce); err != nil {
					conn.Close()
					delete(clients, conn)
				}
			}

		case conn := <-newClients:
			log.Printf("client connected")
			clients[conn] = &client{
				conn: conn,
				enc:  json.NewEncoder(conn),
			}

		case err := <-errCh:
			log.Printf("conntrack error: %v", err)
			return

		case <-sigCh:
			log.Println("shutting down")
			return
		}
	}
}
