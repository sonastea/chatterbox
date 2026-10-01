// Package testrunner provides shared execution for the commands under tests.
package testrunner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Suite string

const (
	Unit        Suite = "unit"
	Integration Suite = "integration"
	E2E         Suite = "e2e"
	Simulations Suite = "simulations"
	Benchmarks  Suite = "benchmarks"
)

// Main runs a suite from the module root, forwarding Go test flags, environment,
// terminal streams, and failures to the caller.
func Main(suite Suite) {
	if err := run(suite, os.Args[1:]); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "run %s: %v\n", suite, err)
		os.Exit(1)
	}
}

func run(suite Suite, flags []string) error {
	mod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return fmt.Errorf("locate module: %w", err)
	}
	moduleFile := strings.TrimSpace(string(mod))
	if moduleFile == "" || moduleFile == os.DevNull {
		return fmt.Errorf("run from the chatterbox checkout")
	}
	root := filepath.Dir(moduleFile)
	program := "go"
	args := []string{"test", "-race", "-count=1", "-v"}
	packages := []string{"./..."}
	switch suite {
	case Unit:
		args = append(args, "-skip", "^Test(Integration|E2E|SimulatedUsers$)")
	case Integration, E2E:
		pattern := "^TestIntegration"
		if suite == E2E {
			pattern = "^TestE2E"
		}
		program = "sh"
		args = []string{filepath.Join(root, "scripts", "test-integration.sh"), "-run", pattern, "-v"}
		packages = nil // The Docker launcher supplies package selection and race flags.
	case Simulations:
		args = append(args, "-run", "^TestSimulatedUsers$")
		packages = []string{"./internal/pkg/box"}
	case Benchmarks:
		args = []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-count=10"}
	default:
		return fmt.Errorf("unknown suite %q", suite)
	}
	args = append(args, flags...)
	args = append(args, packages...)
	cmd := exec.Command(program, args...)
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
