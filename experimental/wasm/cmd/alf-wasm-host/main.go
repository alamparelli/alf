// alf-wasm-host is the spike's CLI entry point.
//
// It loads a manifest + wasm guest, applies the derived Policy, and either
// runs it once (tool) or serves HTTP requests with it (app).
//
// Usage:
//
//	alf-wasm-host run   --manifest path/to/tool-manifest.toml
//	alf-wasm-host serve --manifest path/to/app-manifest.toml [--port 8787] [--frontend DIR]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alamparelli/alf/experimental/wasm/internal/host"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "run":
		if err := runCmd(ctx, args); err != nil {
			log.Fatalf("run: %v", err)
		}
	case "serve":
		if err := serveCmd(ctx, args); err != nil {
			log.Fatalf("serve: %v", err)
		}
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", sub)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `alf-wasm-host — WASM capability runtime (spike)

Subcommands:
  run     Execute a tool once and print its output.
          --manifest PATH    (required) tool manifest.toml
          --data     DIR     host data root (default: ./_data)
          --stdin    STRING  inline stdin (otherwise reads os.Stdin)

  serve   Serve an app over HTTP.
          --manifest PATH    (required) app manifest.toml
          --port     N       listen port (default: 8787)
          --data     DIR     host data root (default: ./_data)
          --frontend DIR     static files served under /static/ (optional)
          --bind     HOST    bind host (default: 127.0.0.1)

Examples:
  alf-wasm-host run   --manifest examples/tool-hello/manifest.toml
  alf-wasm-host serve --manifest examples/app-hello/manifest.toml \
                      --frontend examples/app-hello/frontend
`)
}

// runCmd implements `alf-wasm-host run`.
func runCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to manifest.toml")
	dataRoot := fs.String("data", "./_data", "host data root")
	stdinStr := fs.String("stdin", "", "inline stdin (otherwise reads os.Stdin)")
	_ = fs.Parse(args)

	if *manifest == "" {
		return errors.New("--manifest is required")
	}
	abs, err := filepath.Abs(*manifest)
	if err != nil {
		return err
	}

	rt, err := host.New(ctx, *dataRoot)
	if err != nil {
		return err
	}
	defer rt.Close(ctx)

	var stdin io.Reader = os.Stdin
	if *stdinStr != "" {
		stdin = strings.NewReader(*stdinStr)
	}

	t0 := time.Now()
	stdout, stderr, code, err := rt.InvokeTool(ctx, abs, stdin, nil)
	elapsed := time.Since(t0)
	if err != nil {
		// Still print stderr so policy-denial messages are visible.
		if len(stderr) > 0 {
			fmt.Fprintf(os.Stderr, "--- guest stderr ---\n%s", stderr)
		}
		return err
	}

	fmt.Printf("=== tool output (exit=%d, %.1fms) ===\n", code, float64(elapsed.Microseconds())/1000)
	os.Stdout.Write(stdout)
	if len(stderr) > 0 {
		fmt.Fprintf(os.Stderr, "\n--- guest stderr ---\n%s", stderr)
	}
	if !strings.HasSuffix(string(stdout), "\n") {
		fmt.Println()
	}
	return nil
}

// serveCmd implements `alf-wasm-host serve`.
func serveCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to manifest.toml")
	port := fs.Int("port", 8787, "listen port")
	bind := fs.String("bind", "127.0.0.1", "bind host")
	dataRoot := fs.String("data", "./_data", "host data root")
	frontend := fs.String("frontend", "", "static frontend directory")
	_ = fs.Parse(args)

	if *manifest == "" {
		return errors.New("--manifest is required")
	}
	abs, err := filepath.Abs(*manifest)
	if err != nil {
		return err
	}

	rt, err := host.New(ctx, *dataRoot)
	if err != nil {
		return err
	}
	defer rt.Close(ctx)

	mux := http.NewServeMux()

	// Optional static frontend, mounted under /static/.
	if *frontend != "" {
		absFE, _ := filepath.Abs(*frontend)
		fs := http.FileServer(http.Dir(absFE))
		mux.Handle("/static/", http.StripPrefix("/static/", fs))
		log.Printf("[host] serving static files from %s under /static/", absFE)

		// Root serves index.html from the frontend dir for convenience.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Reserve /api/* for the guest.
			if strings.HasPrefix(r.URL.Path, "/api/") {
				appHandler(ctx, rt, abs, w, r)
				return
			}
			if r.URL.Path == "/" {
				http.ServeFile(w, r, filepath.Join(absFE, "index.html"))
				return
			}
			http.ServeFile(w, r, filepath.Join(absFE, r.URL.Path))
		})
	} else {
		// No frontend — every request goes to the guest.
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			appHandler(ctx, rt, abs, w, r)
		})
	}

	addr := fmt.Sprintf("%s:%d", *bind, *port)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[host] listening on http://%s (manifest=%s)", addr, abs)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func appHandler(ctx context.Context, rt *host.Runtime, manifestPath string, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	t0 := time.Now()
	status, respBody, err := rt.InvokeApp(ctx, manifestPath, r.Method, r.URL.Path, body)
	elapsed := time.Since(t0)

	log.Printf("[host] %s %s -> %d (%.1fms, %d bytes)", r.Method, r.URL.Path, status, float64(elapsed.Microseconds())/1000, len(respBody))

	if err != nil {
		http.Error(w, "guest error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Simple content-type heuristic.
	if len(respBody) > 0 {
		switch {
		case respBody[0] == '{' || respBody[0] == '[':
			w.Header().Set("Content-Type", "application/json")
		case strings.HasPrefix(string(respBody), "<"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	w.WriteHeader(status)
	w.Write(respBody)
}
