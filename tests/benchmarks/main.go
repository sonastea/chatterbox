// Command benchmarks measures performance without race instrumentation.
package main

import "github.com/sonastea/chatterbox/tests/internal/testrunner"

func main() {
	testrunner.Main(testrunner.Benchmarks)
}
