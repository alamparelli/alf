package main

import (
	"fmt"
	"os"
	"time"

	"github.com/alamparelli/alf/cmd/alf/admin"
	"github.com/alamparelli/alf/internal/cli"
)

// Set via -ldflags at build time.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cli.RunInit()
	case "start":
		cli.RunStart()
	case "stop":
		cli.RunStop()
	case "restart":
		cli.RunRestart()
	case "upgrade", "update":
		cli.RunUpgrade(version)
	case "logs":
		cli.RunLogs()
	case "status":
		cli.RunStatus()
	case "secret":
		if len(os.Args) < 3 {
			cli.RunSecretList()
			return
		}
		switch os.Args[2] {
		case "list":
			cli.RunSecretList()
		case "set":
			cli.RunSecretSet(os.Args[3:])
		case "remove":
			cli.RunSecretRemove(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "Unknown secret command: %s\n", os.Args[2])
			fmt.Println("Usage: alf secret [list|set|remove]")
			os.Exit(1)
		}
	case "token":
		if len(os.Args) > 2 && os.Args[2] == "reset" {
			cli.RunTokenReset()
		} else {
			cli.RunToken()
		}
	case "login":
		cli.RunLogin()
	case "magic-link":
		cli.RunMagicLink()
	case "compose":
		cli.RunCompose()
	case "trust":
		runTrust(os.Args[2:])
	case "uninstall":
		cli.RunUninstall()
	case "version":
		fmt.Printf("alf %s\n", version)
		cli.PrintDockerVersion()
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// runTrust resolves the production TrustEnv (real os.Std*, real
// terminal check, real time.Now, real install layout) and dispatches
// to admin.Trust. Errors exit non-zero so shell pipelines (`alf
// trust list && ...`) can chain reliably.
func runTrust(args []string) {
	env := admin.TrustEnv{
		TrustDir:   admin.DefaultTrustDir(),
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		IsTerminal: stdinIsTerminal,
		Now:        time.Now,
	}
	if err := admin.Trust(env, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// stdinIsTerminal reports whether os.Stdin is a TTY. The check is a
// fstat on fd 0; we avoid pulling in golang.org/x/term for the one
// call. Used to gate the mutating trust subcommands per #395 §6.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func printUsage() {
	fmt.Println("Usage: alf <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  init      Interactive setup wizard")
	fmt.Println("  start     Start ALF (docker compose up)")
	fmt.Println("  stop      Stop ALF (docker compose down)")
	fmt.Println("  restart   Restart ALF")
	fmt.Println("  upgrade   Update CLI binary and Docker image")
	fmt.Println("  logs      Follow ALF logs")
	fmt.Println("  status    Show ALF status and versions")
	fmt.Println("  token     Print bearer token for API/mobile access")
	fmt.Println("  login     Authenticate Claude inside the container")
	fmt.Println("  magic-link  Generate a Control Center login link")
	fmt.Println("  secret    Manage secrets (list/set/remove)")
	fmt.Println("  trust     Manage trusted signing keys (list/add/remove/revoke)")
	fmt.Println("  uninstall Remove ALF completely")
	fmt.Println("  version   Print CLI version")
}
