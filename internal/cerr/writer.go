package cerr

import (
	"fmt"
	"io"
	"os"
)

// Writer renders structured errors to stdout (envelope) and stderr (summary).
type Writer struct {
	out      io.Writer
	errw     io.Writer
	exitFunc func(int)
}

// NewDefault returns a Writer bound to os.Stdout, os.Stderr and os.Exit.
func NewDefault() *Writer {
	return &Writer{out: os.Stdout, errw: os.Stderr, exitFunc: os.Exit}
}

// NewWithExit constructs a Writer with injectable outputs and exit function.
func NewWithExit(out, errw io.Writer, exit func(int)) *Writer {
	if exit == nil {
		exit = os.Exit
	}
	return &Writer{out: out, errw: errw, exitFunc: exit}
}

// PrintError writes the JSON envelope to stdout and a one-line summary to stderr.
func (w *Writer) PrintError(err error) {
	e := From(err)
	if e == nil {
		return
	}
	if b, jerr := e.ToJSON(); jerr == nil {
		_, _ = w.out.Write(b)
		_, _ = w.out.Write([]byte("\n"))
	}
	summary := e.Message
	if cause := e.causeText(); cause != "" {
		summary += ": " + cause
	}
	_, _ = fmt.Fprintf(w.errw, "error[%s]: %s\n", e.Kind.String(), summary)
}

// Exit prints the error then exits with its mapped exit code.
func (w *Writer) Exit(err error) {
	e := From(err)
	if e == nil {
		w.exitFunc(ExitCodeOK)
		return
	}
	w.PrintError(e)
	w.exitFunc(e.ExitCode())
}

// Exit prints err via the default Writer and exits with the mapped code.
func Exit(err error) {
	NewDefault().Exit(err)
}

// ExitOK exits the process with code 0.
func ExitOK() {
	os.Exit(ExitCodeOK)
}
