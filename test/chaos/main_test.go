package chaos

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

// helperEnv selects a helper behaviour when the test binary re-executes
// itself as a child process.
const helperEnv = "UNIO_CHAOS_TEST_HELPER"

// helperArgSeparator separates the arguments echoed by the echo helper.
const helperArgSeparator = "\x1f"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "echo" {
		runEchoHelper(os.Args[1:])
	}
	goleak.VerifyTestMain(m)
}

// runEchoHelper prints its arguments separated by helperArgSeparator. When one
// argument is "fail" it writes to stderr and exits with status 3.
func runEchoHelper(args []string) {
	fmt.Fprint(os.Stdout, strings.Join(args, helperArgSeparator))
	for _, a := range args {
		if a == "fail" {
			fmt.Fprint(os.Stderr, "boom")
			os.Exit(3)
		}
	}
	os.Exit(0)
}
