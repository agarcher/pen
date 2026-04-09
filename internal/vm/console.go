package vm

import (
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// AttachConsole connects stdin/stdout to the VM console in raw terminal mode.
// It blocks until the VM's reader returns EOF or an error occurs.
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

	// Handle terminal resize (SIGWINCH) - not applicable to virtio console
	// but we do handle SIGTERM/SIGINT to restore the terminal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	var wg sync.WaitGroup

	// Guest → host stdout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(os.Stdout, vmReader)
	}()

	// Host stdin → guest.
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(vmWriter, os.Stdin)
	}()

	// Wait for signal or guest EOF (the io.Copy from vmReader will return).
	select {
	case <-sigCh:
	case <-waitForGroup(&wg):
	}

	return nil
}

func waitForGroup(wg *sync.WaitGroup) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}
