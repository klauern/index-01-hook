package main

type errorCloser interface {
	Close() error
}

func ignoreCloseError(closer errorCloser) {
	_ = closer.Close()
}
