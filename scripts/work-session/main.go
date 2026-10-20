//go:build unix

// work-session holds one advisory lock across all worktrees of a repository.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errBusy = errors.New("another work session holds the repository lock")

// main reports session errors and returns status 75 when another session holds the lock.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errBusy) {
			os.Exit(75)
		}
		os.Exit(1)
	}
}

// run acquires the shared repository lock and replaces this process with the requested command.
// The replacement process retains the lock and returns its own exit status.
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: work-session COMMAND [ARG ...]")
	}
	common, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return fmt.Errorf("find shared Git directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(strings.TrimSpace(string(common)), "index01-work-session.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open work session lock: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return errBusy
		}
		return fmt.Errorf("acquire work session lock: %w", err)
	}
	command, err := exec.LookPath(args[0])
	if err != nil {
		return err
	}
	// Keep the same process and descriptor across exec. The command owns the lock.
	// Never unlink this file: replacing its inode would allow overlapping locks.
	if _, err := unix.FcntlInt(lock.Fd(), unix.F_SETFD, 0); err != nil {
		return err
	}
	return unix.Exec(command, args, os.Environ())
}
