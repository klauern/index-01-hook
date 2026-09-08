// Command eval-corpus imports private observations without provider access.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

func savePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create private output directory")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create output; choose a new path")
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return errors.New("cannot write output")
	}
	if err := f.Sync(); err != nil {
		return errors.New("cannot sync output")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close output")
	}
	ok = true
	return nil
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("eval-corpus", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "private snapshot or ledger")
	out := flags.String("out", "", "new private corpus path")
	config := flags.String("config", "", "explicit routing configuration")
	if err := flags.Parse(args); err != nil || *input == "" || *out == "" || flags.NArg() != 0 {
		return errors.New("require --input and --out; optional --config")
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return errors.New("cannot read source input")
	}
	corpus, err := evalcorpus.Import(data)
	if err != nil {
		return err
	}
	if *config != "" {
		data, err := os.ReadFile(*config)
		if err != nil {
			return errors.New("cannot read routing configuration")
		}
		corpus.Routing, err = evalcorpus.DecodeRouting(data)
		if err != nil {
			return err
		}
	}
	if err := evalcorpus.Validate(corpus); err != nil {
		return err
	}
	data, err = json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return errors.New("cannot encode corpus")
	}
	if err := savePrivate(*out, append(data, '\n')); err != nil {
		return err
	}
	ready, targets, missingOriginal := 0, 0, 0
	for _, example := range corpus.Examples {
		targets += len(example.Targets)
		if len(evalcorpus.Eligibility(corpus, example)) == 0 {
			ready++
		}
		if example.Input.Text == "" {
			missingOriginal++
		}
	}
	fmt.Fprintf(stdout, "Examples: %d; targets: %d; ready: %d; blocked: %d; missing original transcripts: %d\n", len(corpus.Examples), targets, ready, len(corpus.Examples)-ready, missingOriginal)
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "eval-corpus:", err)
		os.Exit(1)
	}
}
