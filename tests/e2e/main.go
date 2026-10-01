// Command e2e runs complete WebSocket and server lifecycle flows with Docker backends.
package main

import "github.com/sonastea/chatterbox/tests/internal/testrunner"

func main() {
	testrunner.Main(testrunner.E2E)
}
