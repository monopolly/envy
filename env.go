// Package envy loads environment variables into structs.
//
//	type Config struct {
//		Host  string   `env:"http_host" $:"localhost"`
//		Port  int      `@:"http_port" req`
//		Name  string   `!:"angels"`
//		Hosts []string `env:"http_hosts" div:";"`
//		Key   string   `env:"private_key" file:"private.key"`
//		DB    DB       `prefix:"db_"`
//	}
//
//	_ = envy.File(".env.dev")
//	var cfg Config
//	err := envy.Parse(&cfg)
//
// Tags:
//
//	env:"key"   @:"key"          variable name ("-" skips the field)
//	$:"value"   default:"value"  default when the variable is unset or empty
//	!:"value"                    constant; always overrides everything
//	req                          panic if the field ends up without a value
//	file:"path"                  read the value from a file when the variable is unset
//	file                         the variable holds a file path; read its contents
//	prefix:"db_"                 key prefix for nested structs and []struct
//	div:","                      slice / map item separator (default ",")
//	kv:":"                       map key/value separator (default ":")
//
// Precedence: ! > env > file > default.
//
// go vet reports bare tags (`req`, `file`) as "bad syntax for struct tag
// pair". envy parses them fine; to silence vet write `req:""` / `file:""`
// or run `go vet -structtag=false`.
package envy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Parse fills the struct pointed to by v from environment variables.
// It returns parse errors, and panics if a `req` field has no value.
func Parse(v any) error {
	return (&parser{}).run(v)
}

// ---- tags ----

// parseTag splits a struct tag into key:"value" pairs and bare flags (`req`).
func parseTag(tag string) (map[string]string, error) {
	var m map[string]string
	for {
		tag = strings.TrimLeft(tag, " \t")
		if tag == "" {
			return m, nil
		}
		i := 0
		for i < len(tag) && tag[i] > ' ' && tag[i] != ':' && tag[i] != '"' && tag[i] != 0x7f {
			i++
		}
		if i == 0 {
			return nil, fmt.Errorf("bad tag syntax near %q", tag)
		}
		name := tag[:i]
		tag = tag[i:]

		val := ""
		if tag != "" && tag[0] != ' ' && tag[0] != '\t' {
			if len(tag) < 2 || tag[0] != ':' || tag[1] != '"' {
				return nil, fmt.Errorf("bad tag syntax near %q", name+tag)
			}
			tag = tag[1:]
			j := 1
			for j < len(tag) && tag[j] != '"' {
				if tag[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(tag) {
				return nil, fmt.Errorf("unterminated tag value for %q", name)
			}
			var err error
			if val, err = strconv.Unquote(tag[:j+1]); err != nil {
				return nil, fmt.Errorf("bad tag value for %q: %w", name, err)
			}
			tag = tag[j+1:]
		}
		if m == nil {
			m = make(map[string]string, 4)
		}
		if _, dup := m[name]; !dup {
			m[name] = val
		}
	}
}

// first returns the value of the first present tag name.
func first(m map[string]string, names ...string) (string, bool) {
	for _, n := range names {
		if v, ok := m[n]; ok {
			return v, true
		}
	}
	return "", false
}

// ---- field plans (cached per type) ----

type fieldKind uint8

const (
	kindLeaf        fieldKind = iota
	kindStruct                // named struct: own value, then recurse
	kindAnon                  // unnamed struct literal: recurse only
	kindPtrStruct             // *struct: recurse if non-nil
	kindSliceStruct           // []struct or *[]struct
)

type fieldPlan struct {
	idx     int
	name    string
	typ     reflect.Type
	key     string
	def     string
	hasDef  bool
	val     string
	hasVal  bool
	file    string
	hasFile bool
	req     bool
	ignored bool
	prefix  string
	sep     string
	kvSep   string
	kind    fieldKind
	value   bool // parsed as a single value; never recursed into
	err     error
}

var plans sync.Map // reflect.Type -> []fieldPlan

func planFor(t reflect.Type) []fieldPlan {
	if p, ok := plans.Load(t); ok {
		return p.([]fieldPlan)
	}
	p, _ := plans.LoadOrStore(t, buildPlan(t))
	return p.([]fieldPlan)
}

func buildPlan(t reflect.Type) []fieldPlan {
	out := make([]fieldPlan, 0, t.NumField())
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		f := fieldPlan{
			idx:   i,
			name:  sf.Name,
			typ:   sf.Type,
			sep:   ",",
			kvSep: ":",
			value: isValueType(sf.Type),
		}

		ft := sf.Type
		switch {
		case ft.Kind() == reflect.Struct && ft.Name() == "":
			f.kind = kindAnon
		case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
			f.kind = kindPtrStruct
		case ft.Kind() == reflect.Struct:
			f.kind = kindStruct
		case isSliceOfStructs(ft):
			f.kind = kindSliceStruct
		}

		tags, err := parseTag(string(sf.Tag))
		if err != nil {
			f.err = tagError{Field: sf.Name, Err: err}
			out = append(out, f)
			continue
		}
		f.key, _ = first(tags, "env", "@")
		f.ignored = f.key == "-"
		f.def, f.hasDef = first(tags, "$", "default")
		f.val, f.hasVal = tags["!"]
		f.file, f.hasFile = tags["file"]
		_, f.req = tags["req"]
		f.prefix = tags["prefix"]
		if s := tags["div"]; s != "" {
			f.sep = s
		}
		if s := tags["kv"]; s != "" {
			f.kvSep = s
		}
		out = append(out, f)
	}
	return out
}

func isSliceOfStructs(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct
}

// ---- parser ----

type parser struct {
	env     map[string]string // nil: process environment
	keys    []string          // env keys, loaded lazily for []struct
	keysOK  bool
	missing []string // req fields without a value
}

func (a *parser) run(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return errNotStructPtr
	}
	err := a.parseStruct(rv.Elem(), "")
	if len(a.missing) > 0 {
		panic("envy: required values not set: " + strings.Join(a.missing, ", "))
	}
	return err
}

func (a *parser) lookup(key string) (string, bool) {
	if a.env != nil {
		v, ok := a.env[key]
		return v, ok
	}
	return os.LookupEnv(key)
}

func (a *parser) envKeys() []string {
	if a.keysOK {
		return a.keys
	}
	a.keysOK = true
	if a.env != nil {
		a.keys = make([]string, 0, len(a.env))
		for k := range a.env {
			a.keys = append(a.keys, k)
		}
		return a.keys
	}
	env := os.Environ()
	a.keys = make([]string, 0, len(env))
	for _, e := range env {
		if k, _, ok := strings.Cut(e, "="); ok {
			a.keys = append(a.keys, k)
		}
	}
	return a.keys
}

func (a *parser) parseStruct(rv reflect.Value, prefix string) error {
	plan := planFor(rv.Type())
	var errs []error
	for i := range plan {
		if err := a.field(rv.Field(plan[i].idx), &plan[i], prefix); err != nil {
			var agg aggregateError
			if errors.As(err, &agg) {
				errs = append(errs, agg...)
			} else {
				errs = append(errs, err)
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return aggregateError(errs)
}

func (a *parser) field(v reflect.Value, f *fieldPlan, prefix string) error {
	switch f.kind {
	case kindAnon:
		return a.parseStruct(v, prefix+f.prefix)
	case kindPtrStruct:
		if !v.IsNil() && !f.value {
			return a.parseStruct(v.Elem(), prefix+f.prefix)
		}
	}
	if f.err != nil {
		return f.err
	}
	if f.ignored {
		return nil
	}
	if err := a.resolve(v, f, prefix); err != nil {
		return err
	}
	if f.value {
		return nil
	}
	switch f.kind {
	case kindStruct:
		return a.parseStruct(v, prefix+f.prefix)
	case kindSliceStruct:
		return a.parseSlice(v, prefix+f.prefix)
	}
	return nil
}

// resolve finds the field value (v > env > file > default) and assigns it.
func (a *parser) resolve(v reflect.Value, f *fieldPlan, prefix string) error {
	if f.hasVal {
		if f.val == "" {
			v.SetZero()
			return nil
		}
		return a.assign(v, f, f.val)
	}

	var val string
	var ok bool
	key := ""
	if f.key != "" {
		key = prefix + f.key
		val, ok = a.lookup(key)
		ok = ok && val != ""
	}

	if f.hasFile {
		path := f.file
		if path == "" && ok { // bare `file`: the variable holds the path
			path, ok = val, false
		}
		if !ok && path != "" {
			b, err := os.ReadFile(path)
			switch {
			case err == nil:
				val, ok = string(b), true
			case errors.Is(err, fs.ErrNotExist) && (f.hasDef || f.req):
				// fall through to default / req
			default:
				return fileError{Field: f.name, Path: path, Err: err}
			}
		}
	}

	if !ok && f.hasDef {
		val, ok = f.def, true
	}
	if !ok {
		if f.req {
			name := key
			if name == "" {
				name = f.name
			}
			a.missing = append(a.missing, name)
		}
		return nil
	}
	if val == "" {
		return nil
	}
	return a.assign(v, f, val)
}

// parseSlice fills a []struct from PREFIX_0_KEY, PREFIX_1_KEY, ...
func (a *parser) parseSlice(v reflect.Value, prefix string) error {
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}

	var found bool
	var seen []bool
	for _, k := range a.envKeys() {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		found = true
		rest := k[len(prefix):]
		j := strings.IndexByte(rest, '_')
		if j <= 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:j])
		if err != nil || n < 0 || n > 1<<16 || strconv.Itoa(n) != rest[:j] {
			continue
		}
		if n >= len(seen) {
			seen = append(seen, make([]bool, n+1-len(seen))...)
		}
		seen[n] = true
	}
	if !found {
		return nil
	}
	count := 0
	for count < len(seen) && seen[count] {
		count++
	}

	sliceType := v.Type()
	initial := v
	if v.Kind() == reflect.Pointer {
		sliceType = sliceType.Elem()
		initial = v.Elem()
	}
	n := 0
	if initial.IsValid() {
		n = initial.Len()
	}
	size := max(n, count)
	if size == 0 {
		return nil
	}

	out := reflect.MakeSlice(sliceType, size, size)
	if n > 0 {
		reflect.Copy(out, initial)
	}
	for i := range size {
		if err := a.parseStruct(out.Index(i), prefix+strconv.Itoa(i)+"_"); err != nil {
			return err
		}
	}

	if v.Kind() == reflect.Pointer {
		ptr := reflect.New(sliceType)
		ptr.Elem().Set(out)
		out = ptr
	}
	v.Set(out)
	return nil
}
