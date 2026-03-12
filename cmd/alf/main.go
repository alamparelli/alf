package main

import (
	"fmt"
	"os"

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
	fmt.Println("  uninstall Remove ALF completely")
	fmt.Println("  version   Print CLI version")
}
