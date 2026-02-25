package cli

import (
	"fmt"
	"os"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

const banner = `
     █████╗ ██╗     ███████╗
    ██╔══██╗██║     ██╔════╝
    ███████║██║     █████╗
    ██╔══██║██║     ██╔══╝
    ██║  ██║███████╗██║
    ╚═╝  ╚═╝╚══════╝╚═╝
`

func PrintBanner() {
	fmt.Print(colorCyan + banner + colorReset)
}

func PrintStep(num int, title string) {
	fmt.Printf("\n%s[Step %d]%s %s%s%s\n", colorCyan, num, colorReset, colorBold, title, colorReset)
}

func PrintCheck(msg string) {
	fmt.Printf("  %s✓%s %s\n", colorGreen, colorReset, msg)
}

func PrintWarning(msg string) {
	fmt.Printf("  %s⚠%s %s\n", colorYellow, colorReset, msg)
}

func PrintError(msg string) {
	fmt.Printf("  %s✗%s %s\n", colorRed, colorReset, msg)
}

func PrintInfo(msg string) {
	fmt.Printf("  %s→%s %s\n", colorDim, colorReset, msg)
}

func PrintSuccess(msg string) {
	fmt.Printf("\n%s%s%s\n", colorGreen, msg, colorReset)
}

func Fatal(msg string) {
	PrintError(msg)
	os.Exit(1)
}
