//go:build linux

// nettrack-helper is a privileged helper that subscribes to conntrack NEW events
// and writes them as JSON lines to a Unix socket. It runs as root with CAP_NET_ADMIN
// before the daemon drops capabilities via setpriv.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/alamparelli/alf/internal/platform/socknet"
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
const ctrlSockPath = "/run/alf-nettrack-ctrl.sock"

// killSwitchChain is the iptables chain used to enforce the kill switch.
const killSwitchChain = "ALF_KILLSWITCH"

// initKillSwitchChain creates the iptables chain and ensures it is jumped from OUTPUT.
// Starts empty (all traffic allowed) — clean state on every restart.
func initKillSwitchChain() {
	exec.Command("iptables", "-N", killSwitchChain).Run() // ignore error if chain exists
	exec.Command("iptables", "-F", killSwitchChain).Run() // flush to start clean
	// Remove any stale jump rules, then add exactly one.
	for {
		if exec.Command("iptables", "-D", "OUTPUT", "-j", killSwitchChain).Run() != nil {
			break
		}
	}
	if err := exec.Command("iptables", "-I", "OUTPUT", "1", "-j", killSwitchChain).Run(); err != nil {
		log.Printf("iptables chain setup: %v", err)
	} else {
		log.Printf("[nettrack] iptables %s chain ready (empty = traffic allowed)", killSwitchChain)
	}
}

// applyKillSwitch enables or disables network blocking via iptables.
// When enabled, all non-loopback outbound traffic is rejected.
func applyKillSwitch(enable bool) error {
	// Flush chain first to avoid duplicate rules.
	if err := exec.Command("iptables", "-F", killSwitchChain).Run(); err != nil {
		return err
	}
	if enable {
		// Allow loopback, reject everything else outbound.
		exec.Command("iptables", "-A", killSwitchChain, "-o", "lo", "-j", "RETURN").Run()
		if err := exec.Command("iptables", "-A", killSwitchChain, "-j", "REJECT",
			"--reject-with", "icmp-port-unreachable").Run(); err != nil {
			return err
		}
		log.Println("[nettrack] KILL SWITCH ENABLED — iptables blocking all outbound traffic")
	} else {
		log.Println("[nettrack] kill switch disabled — outbound traffic restored")
	}
	return nil
}

// ctrlMsg is the JSON command sent by the daemon's NetTracker.
type ctrlMsg struct {
	KillSwitch bool `json:"kill_switch"`
}

// runControlSocket listens for kill switch commands from the daemon.
//
// SEC-080-003: ListenUnix0660 narrows the umask around net.Listen so the
// socket inode is mode 0660 from byte zero, closing the TOCTOU window
// between Listen and the legacy os.Chmod where the LLM (uid 1000) could
// have connected during the kernel-default 0o775 phase and toggled the
// iptables kill switch.
func runControlSocket(ctx context.Context) {
	os.Remove(ctrlSockPath)
	ln, err := socknet.ListenUnix0660(ctrlSockPath, 1001) // chgrp alfd
	if err != nil {
		log.Printf("[nettrack] control socket: %v", err)
		return
	}
	// chown root:alfd — ListenUnix0660 already chgrp'd to 1001.
	_ = os.Chown(ctrlSockPath, 0, 1001)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	log.Printf("[nettrack] control socket ready at %s", ctrlSockPath)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			dec := json.NewDecoder(c)
			for {
				var msg ctrlMsg
				if err := dec.Decode(&msg); err != nil {
					return
				}
				if err := applyKillSwitch(msg.KillSwitch); err != nil {
					log.Printf("[nettrack] killswitch iptables error: %v", err)
				}
			}
		}(conn)
	}
}

func main() {
	log.SetPrefix("[nettrack] ")
	log.SetFlags(log.Ltime)

	// Clean up stale socket.
	os.Remove(sockPath)

	// SEC-080-003: same TOCTOU concern as the control socket above —
	// the events socket carries every outbound conntrack record. A
	// process that connected during the kernel-default 0o775 phase
	// could have read every NEW connection the daemon was about to
	// firewall-classify.
	ln, err := socknet.ListenUnix0660(sockPath, 1001) // chgrp alfd
	if err != nil {
		log.Fatalf("listen %s: %v", sockPath, err)
	}
	defer ln.Close()
	_ = os.Chown(sockPath, 0, 1001) // root:alfd

	log.Printf("listening on %s", sockPath)

	// Set up iptables kill switch chain (clean state on start).
	initKillSwitchChain()

	// Start control socket for kill switch commands from daemon.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runControlSocket(ctx)

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
			cancel()
			// Ensure kill switch is off before exiting so traffic is restored.
			applyKillSwitch(false)
			return
		}
	}
}
