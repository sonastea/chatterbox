// Command unit runs isolated logic and component tests from this checkout.
package main

import "github.com/sonastea/chatterbox/tests/internal/testrunner"

func main() {
	testrunner.Main(testrunner.Unit)
}
