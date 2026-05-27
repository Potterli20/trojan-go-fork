package common

import (
	"fmt"
	"os"
	"runtime/debug"
)

type Error struct {
	info  string
	cause error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return e.info + " | " + e.cause.Error()
	}
	return e.info
}

// Unwrap returns the underlying cause of the error, enabling errors.Is and errors.As compatibility
func (e *Error) Unwrap() error {
	return e.cause
}

func (e *Error) Base(err error) *Error {
	e.cause = err
	return e
}

func NewError(info string) *Error {
	return &Error{
		info: info,
	}
}

func NewErrorf(format string, a ...any) *Error {
	return NewError(fmt.Sprintf(format, a...))
}

func Must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL ERROR:", err)
		os.Stderr.WriteString("Stack trace:\n")
		debug.PrintStack()
		panic(err)
	}
}

func Must2(_ any, err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "FATAL ERROR:", err)
		os.Stderr.WriteString("Stack trace:\n")
		debug.PrintStack()
		panic(err)
	}
}
