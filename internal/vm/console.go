package vm

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// AttachConsole connects stdin/stdout to the VM console in raw terminal mode.
// It returns when the VM's reader closes (guest exited) or a signal is received.
// The stdin-copy goroutine is intentionally not waited on — io.Copy on os.Stdin
// can block indefinitely after the guest exits, since the host stdin remains open.
// The terminal is restored to its original state on return.
func AttachConsole(vmReader io.Reader, vmWriter io.Writer) error {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		oldState, err := term.MakeRaw(fd)
		if err != nil {
			return err
		}
		defer term.Restore(fd, oldState)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	guestDone := make(chan struct{})

	// Guest → host stdout. Closes guestDone when the guest closes its output.
	go func() {
		_, _ = io.Copy(os.Stdout, vmReader)
		close(guestDone)
	}()

	// Host stdin → guest. Not awaited: a blocked Read on os.Stdin would
	// otherwise prevent return after guest exit. The goroutine is leaked
	// for the brief lifetime of the process.
	go func() {
		_, _ = io.Copy(vmWriter, os.Stdin)
	}()

	select {
	case <-sigCh:
	case <-guestDone:
	}

	return nil
}
