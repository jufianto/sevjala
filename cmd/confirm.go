package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/jufianto/sevjala/localapi"
)

// withConfirm runs call with the word given by --confirm. When the agent
// answers that the change needs confirmation and none was given, it shows the
// agent's message and asks for the word once; anything else cancels.
func withConfirm[T any](in io.Reader, out io.Writer, given string, call func(confirm string) (T, error)) (T, error) {
	v, err := call(given)
	e, ok := localapi.AsConfirm(err)
	if !ok {
		return v, err
	}
	if given != "" {
		return v, failf("--confirm %q is not the word: %w; to go ahead use --confirm %s", given, err, e.Word)
	}
	fmt.Fprintf(out, "%s.\nType %s to continue, anything else cancels: ", e.Msg, e.Word)
	line, _ := bufio.NewReader(in).ReadString('\n')
	if strings.TrimSpace(line) != e.Word {
		fmt.Fprintln(out, "Cancelled: nothing was changed.")
		var zero T
		return zero, errSilent1
	}
	return call(e.Word)
}
