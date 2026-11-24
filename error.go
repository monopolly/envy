package envy

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

var (
	errNotStructPtr = errors.New("envy: expected a pointer to a struct")
	errNoParser     = errors.New("no parser")
)

// aggregateError combines all field errors of a parse.
type aggregateError []error

func (a aggregateError) Error() string {
	var b strings.Builder
	b.WriteString("envy:")
	for i, err := range a {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteByte(' ')
		b.WriteString(err.Error())
	}
	return b.String()
}

func (a aggregateError) Unwrap() []error {
	return a
}

type parseError struct {
	Field string
	Type  reflect.Type
	Err   error
}

func (a parseError) Error() string {
	return fmt.Sprintf("field %s (%s): %v", a.Field, a.Type, a.Err)
}

func (a parseError) Unwrap() error {
	return a.Err
}

type noParserError struct {
	Field string
	Type  reflect.Type
}

func (a noParserError) Error() string {
	return fmt.Sprintf("field %s: unsupported type %s", a.Field, a.Type)
}

type tagError struct {
	Field string
	Err   error
}

func (a tagError) Error() string {
	return fmt.Sprintf("field %s: %v", a.Field, a.Err)
}

func (a tagError) Unwrap() error {
	return a.Err
}

type fileError struct {
	Field string
	Path  string
	Err   error
}

func (a fileError) Error() string {
	return fmt.Sprintf("field %s: read file %q: %v", a.Field, a.Path, a.Err)
}

func (a fileError) Unwrap() error {
	return a.Err
}

type dotenvError struct {
	Name string
	Line int
	Msg  string
}

func (a dotenvError) Error() string {
	if a.Name == "" {
		return fmt.Sprintf("envy: line %d: %s", a.Line, a.Msg)
	}
	return fmt.Sprintf("envy: %s:%d: %s", a.Name, a.Line, a.Msg)
}
