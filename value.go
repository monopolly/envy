package envy

import (
	"encoding"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	durationType        = reflect.TypeFor[time.Duration]()
	urlType             = reflect.TypeFor[url.URL]()
	locationType        = reflect.TypeFor[time.Location]()
)

// isValueType reports whether t (or *t) is parsed as a single value
// rather than a struct to recurse into.
func isValueType(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t {
	case urlType, locationType, durationType:
		return true
	}
	return reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// assign parses s into the field v.
func (a *parser) assign(v reflect.Value, f *fieldPlan, s string) error {
	err := setValue(v, s, f, true)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errNoParser):
		return noParserError{Field: f.name, Type: f.typ}
	default:
		return parseError{Field: f.name, Type: f.typ, Err: err}
	}
}

// setValue parses s into v. top allows slice and map containers.
func setValue(v reflect.Value, s string, f *fieldPlan, top bool) error {
	t := v.Type()

	if t.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(t.Elem()))
		}
		return setValue(v.Elem(), s, f, top)
	}

	if t.Kind() != reflect.Interface && reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
	}

	switch t {
	case durationType:
		d, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	case urlType:
		u, err := url.Parse(s)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(*u))
		return nil
	case locationType:
		loc, err := time.LoadLocation(s)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(loc).Elem())
		return nil
	}

	switch t.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, t.Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, t.Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, t.Bits())
		if err != nil {
			return err
		}
		v.SetFloat(n)
	case reflect.Slice:
		if !top {
			return errNoParser
		}
		return setSlice(v, s, f)
	case reflect.Map:
		if !top {
			return errNoParser
		}
		return setMap(v, s, f)
	default:
		return errNoParser
	}
	return nil
}

func setSlice(v reflect.Value, s string, f *fieldPlan) error {
	parts := strings.Split(s, f.sep)
	out := reflect.MakeSlice(v.Type(), len(parts), len(parts))
	for i, part := range parts {
		if err := setValue(out.Index(i), strings.TrimSpace(part), f, false); err != nil {
			return err
		}
	}
	v.Set(out)
	return nil
}

func setMap(v reflect.Value, s string, f *fieldPlan) error {
	t := v.Type()
	parts := strings.Split(s, f.sep)
	out := reflect.MakeMapWithSize(t, len(parts))
	key := reflect.New(t.Key()).Elem()
	elem := reflect.New(t.Elem()).Elem()
	for _, part := range parts {
		k, e, ok := strings.Cut(part, f.kvSep)
		if !ok {
			return fmt.Errorf("%q should be in \"key%svalue\" format", part, f.kvSep)
		}
		key.SetZero()
		elem.SetZero()
		if err := setValue(key, strings.TrimSpace(k), f, false); err != nil {
			return err
		}
		if err := setValue(elem, strings.TrimSpace(e), f, false); err != nil {
			return err
		}
		out.SetMapIndex(key, elem)
	}
	v.Set(out)
	return nil
}
