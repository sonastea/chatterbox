// Command integration runs database and broker contracts with Docker backends.
package main

import "github.com/sonastea/chatterbox/tests/internal/testrunner"

func main() {
	testrunner.Main(testrunner.Integration)
}
