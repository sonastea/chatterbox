// Command simulations runs the WebSocket user simulation from this checkout.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	// Run the same test used by CI, with caching disabled so every invocation
	// exercises the server. SIM_* configuration is inherited from the environment.
	args := []string{"test", "-race", "-count=1", "-run", "^TestSimulatedUsers$", "-v"}
	args = append(args, os.Args[1:]...)
	args = append(args, "github.com/sonastea/chatterbox/internal/pkg/box")

	cmd := exec.Command("go", args...)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			os.Exit(exitErr.ExitCode())
		}

		fmt.Fprintln(os.Stderr, "run simulation:", err)
		os.Exit(1)
	}
}
