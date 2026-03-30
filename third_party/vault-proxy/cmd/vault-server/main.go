package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alamparelli/vault-proxy/internal/api"
	"github.com/alamparelli/vault-proxy/internal/vault"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8390", "address to listen on")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for vault.enc")
	tokenTTL := flag.Duration("token-ttl", 24*time.Hour, "session token TTL")
	tlsCert := flag.String("tls-cert", "", "path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "path to TLS private key file")
	httpProxy := flag.String("http-proxy", "", "HTTP proxy URL for outbound requests (e.g. http://127.0.0.1:4751)")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	// Ignore SIGPIPE to prevent crashes when stdout/stderr pipe breaks
	// (common in containerized environments where logging pipes can close).
	signal.Ignore(syscall.SIGPIPE)

	store := vault.NewStore(*dataDir)
	server := api.NewServer(store, *tokenTTL, *httpProxy)

	srv := &http.Server{
		Handler:      server,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on SIGTERM/SIGINT.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("vault-proxy received %s, shutting down...", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	// Bind listener — Unix socket or TCP.
	var (
		ln  net.Listener
		err error
	)
	if strings.HasPrefix(*listen, "unix:") {
		sockPath := strings.TrimPrefix(*listen, "unix:")
		os.Remove(sockPath) // clear stale socket
		ln, err = net.Listen("unix", sockPath)
		if err != nil {
			log.Fatalf("bind unix %s: %v", sockPath, err)
		}
		os.Chmod(sockPath, 0660)
		log.Printf("vault-proxy listening on unix:%s (data: %s)", sockPath, *dataDir)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	} else {
		// Safety check: refuse non-loopback bind without TLS
		if *tlsCert == "" && !isLoopback(*listen) {
			log.Fatalf("refusing to bind to %s without TLS — master password would be sent in cleartext. Use --tls-cert and --tls-key, or bind to 127.0.0.1", *listen)
		}
		srv.Addr = *listen
		ln, err = net.Listen("tcp", *listen)
		if err != nil {
			log.Fatalf("bind %s: %v", *listen, err)
		}
		if *tlsCert != "" && *tlsKey != "" {
			srv.TLSConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
			log.Printf("vault-proxy listening on %s (TLS, data: %s)", *listen, *dataDir)
			tlsLn := tls.NewListener(ln, srv.TLSConfig)
			if err := srv.ServeTLS(tlsLn, *tlsCert, *tlsKey); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		} else {
			log.Printf("vault-proxy listening on %s (data: %s)", *listen, *dataDir)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server error: %v", err)
			}
		}
	}

	log.Println("vault-proxy stopped")
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".vault-proxy"
	}
	return fmt.Sprintf("%s/.vault-proxy", home)
}

// isLoopback checks if the listen address binds to loopback only.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	if host == "" {
		return false // empty host = 0.0.0.0 = all interfaces, not loopback
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
