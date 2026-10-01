// Command simulations runs the WebSocket user simulation from this checkout.
package main

import "github.com/sonastea/chatterbox/tests/internal/testrunner"

func main() {
	testrunner.Main(testrunner.Simulations)
}
