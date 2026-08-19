package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: rename-no-replace SOURCE DESTINATION")
		os.Exit(2)
	}
	if err := renameNoReplace(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "release output publication failed")
		os.Exit(1)
	}
}
