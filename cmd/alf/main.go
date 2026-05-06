package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

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
		runAdmin(admin.Trust, os.Args[2:])
	case "keygen":
		runAdmin(admin.Keygen, os.Args[2:])
	case "sign":
		runAdmin(admin.Sign, os.Args[2:])
	case "pending":
		runAdmin(admin.Pending, os.Args[2:])
	case "ratify":
		runAdmin(admin.Ratify, os.Args[2:])
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

// runAdmin resolves the production admin.Env (real os.Std*, real
// terminal check, real time.Now, real install layout, x/term-backed
// passphrase reader) and dispatches to handler. Errors exit non-zero
// so shell pipelines (`alf trust list && ...`) chain reliably.
//
// Single env constructor for trust + keygen + sign + (later) pending
// + ratify keeps the production wiring in one place. A new admin
// handler only needs a one-line case in main.
func runAdmin(handler func(admin.Env, []string) error, args []string) {
	env := admin.Env{
		TrustDir:     admin.DefaultTrustDir(),
		UserKeyPath:  admin.DefaultUserKeyPath(),
		PendingDir:   admin.DefaultPendingDir(),
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		IsTerminal:   stdinIsTerminal,
		Now:          time.Now,
		ReadPassword: readPasswordFromTTY,
	}
	if err := handler(env, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// stdinIsTerminal reports whether os.Stdin is a TTY. Used to gate
// the mutating admin subcommands per #395 §6.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// readPasswordFromTTY prints prompt to stdout and reads a passphrase
// from stdin without echo. Falls back to an explicit error if stdin
// is not a real terminal — admin.Env.IsTerminal already gates the
// command before we get here, so this is defence in depth.
func readPasswordFromTTY(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stdout, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stdout) // newline after silent read
	if err != nil {
		return nil, err
	}
	return pw, nil
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
	fmt.Println("  keygen    Mint the user-endorsed signing key (passphrase-protected)")
	fmt.Println("  sign      Sign a bundle with the user-endorsed key")
	fmt.Println("  pending   List items awaiting ratification")
	fmt.Println("  ratify    Approve or deny a pending item")
	fmt.Println("  uninstall Remove ALF completely")
	fmt.Println("  version   Print CLI version")
}
