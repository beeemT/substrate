// keytest prints the raw bytes received for each keypress.
// Run in Warp with: go run ./cmd/keytest
// Press the keys you want to inspect, then press Ctrl+C to exit.
package main

import (
	"fmt"
	"os"

	xterm "github.com/charmbracelet/x/term"
)

func main() {
	if !xterm.IsTerminal(os.Stdin.Fd()) {
		fmt.Fprintln(os.Stderr, "stdin is not a terminal")
		os.Exit(1)
	}

	old, err := xterm.MakeRaw(os.Stdin.Fd())
	if err != nil {
		fmt.Fprintln(os.Stderr, "MakeRaw:", err)
		os.Exit(1)
	}
	defer xterm.Restore(os.Stdin.Fd(), old) //nolint:errcheck

	fmt.Print("Press keys (Ctrl+C to quit):\r\n")

	buf := make([]byte, 64)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			fmt.Printf("read error: %v\r\n", err)
			return
		}

		b := buf[:n]

		// Ctrl+C = exit
		if n == 1 && b[0] == 3 {
			fmt.Print("bye\r\n")
			return
		}

		fmt.Printf("hex: % X   quoted: %q\r\n", b, b)
	}
}
