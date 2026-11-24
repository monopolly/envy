package envy

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// parseEnv parses with an explicit environment instead of the process one.
func parseEnv(v any, env map[string]string) error {
	return (&parser{env: env}).run(v)
}

func eq[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func isErr[E error](t *testing.T, err error) E {
	t.Helper()
	var e E
	if !errors.As(err, &e) {
		t.Fatalf("expected %T, got %v", e, err)
	}
	return e
}

func mustPanic(t *testing.T, contains string, fn func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if !strings.Contains(fmt.Sprint(r), contains) {
			t.Fatalf("panic %q does not contain %q", r, contains)
		}
	}()
	fn()
}

// structWithTags builds a one-field-per-tag struct type at runtime
// (used when a tag must contain a temp path).
func structWithTags(tags ...string) reflect.Type {
	fields := make([]reflect.StructField, len(tags))
	for i, tag := range tags {
		fields[i] = reflect.StructField{
			Name: fmt.Sprintf("F%d", i),
			Type: reflect.TypeFor[string](),
			Tag:  reflect.StructTag(tag),
		}
	}
	return reflect.StructOf(fields)
}

type Config struct {
	Host  string   `env:"http_host"`
	Port  int      `@:"http_port"`
	Name  string   `!:"angels"`
	Hosts []string `env:"http_hosts"`
}

func TestExampleConfig(t *testing.T) {
	t.Setenv("http_host", "example.com")
	t.Setenv("http_port", "8080")
	t.Setenv("http_hosts", "a.com, b.com,c.com")

	var cfg Config
	noErr(t, Parse(&cfg))
	eq(t, cfg, Config{
		Host:  "example.com",
		Port:  8080,
		Name:  "angels",
		Hosts: []string{"a.com", "b.com", "c.com"},
	})
}

func TestParseTag(t *testing.T) {
	m, err := parseTag(`env:"a" req $:"x y" file  json:"q,omitempty" div:"\"" env:"dup"`)
	noErr(t, err)
	eq(t, m, map[string]string{"env": "a", "req": "", "$": "x y", "file": "", "json": "q,omitempty", "div": `"`})

	m, err = parseTag("")
	noErr(t, err)
	eq(t, len(m), 0)

	for _, bad := range []string{`env:`, `env:x`, `env:"abc`, `:"x"`, `env:"\q"`} {
		if _, err := parseTag(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestTagAliases(t *testing.T) {
	type C struct {
		A string `env:"A"`
		B string `@:"B"`
		C string `env:"C" @:"OTHER"`
		D string `$:"d1"`
		E string `default:"e1"`
		F string `env:"F" $:"f1"`
		G string `env:"G" default:"g1"`
	}
	var c C
	noErr(t, parseEnv(&c, map[string]string{"A": "a", "B": "b", "C": "c", "OTHER": "o", "G": "g"}))
	eq(t, c, C{A: "a", B: "b", C: "c", D: "d1", E: "e1", F: "f1", G: "g"})
}

func TestConstantOverridesEverything(t *testing.T) {
	type C struct {
		A string        `env:"A" $:"def" !:"const"`
		B int           `env:"B" req !:"42"`
		C time.Duration `!:"1m30s"`
		D []int         `!:"1;2;3" div:";"`
		E string        `env:"E" !:""`
		F *int          `!:"7"`
		G string        `env:"G" file:"/nonexistent" !:"g"`
	}
	c := C{E: "preset"}
	noErr(t, parseEnv(&c, map[string]string{"A": "env", "B": "1", "E": "env", "G": "env"}))
	seven := 7
	eq(t, c, C{A: "const", B: 42, C: 90 * time.Second, D: []int{1, 2, 3}, E: "", F: &seven, G: "g"})

	type B struct {
		A int `!:"nope"`
	}
	isErr[parseError](t, parseEnv(&B{}, nil))
}

func TestReq(t *testing.T) {
	type C struct {
		A string `env:"A" req`
		B string `env:"B" req:""`
		C string `env:"C" req $:"x"`
		D string `env:"D" req`
		E string `req`
		F string `env:"F" req`
	}
	var c C
	mustPanic(t, "A, B, D, E", func() {
		_ = parseEnv(&c, map[string]string{"D": "", "F": "f"})
	})
	eq(t, c.C, "x")
	eq(t, c.F, "f") // other fields are filled before the panic
}

func TestReqSatisfied(t *testing.T) {
	type C struct {
		A string `env:"A" req`
		B string `req !:"const"`
		C string `req $:"def"`
	}
	var c C
	noErr(t, parseEnv(&c, map[string]string{"A": "a"}))
	eq(t, c, C{"a", "const", "def"})
}

func TestReqWithPrefix(t *testing.T) {
	type DB struct {
		Host string `env:"HOST" req`
	}
	type C struct {
		DB DB `prefix:"DB_"`
	}
	mustPanic(t, "DB_HOST", func() { _ = parseEnv(&C{}, map[string]string{}) })
}

func TestFileTag(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "private.key")
	other := filepath.Join(dir, "other.key")
	missing := filepath.Join(dir, "missing")
	noErr(t, os.WriteFile(key, []byte("KEY-FROM-FILE"), 0o600))
	noErr(t, os.WriteFile(other, []byte("OTHER"), 0o600))

	typ := structWithTags(
		`env:"K1" file:`+strconv.Quote(key),                // env unset -> file
		`env:"K2" file:`+strconv.Quote(key),                // env set -> env wins
		`file:`+strconv.Quote(key),                         // no key
		`env:"K3" file`,                                    // env holds the path
		`env:"K4" file:`+strconv.Quote(missing)+` $:"def"`, // missing file -> default
	)
	ptr := reflect.New(typ)
	noErr(t, parseEnv(ptr.Interface(), map[string]string{"K2": "env", "K3": other}))
	got := ptr.Elem()
	eq(t, got.Field(0).String(), "KEY-FROM-FILE")
	eq(t, got.Field(1).String(), "env")
	eq(t, got.Field(2).String(), "KEY-FROM-FILE")
	eq(t, got.Field(3).String(), "OTHER")
	eq(t, got.Field(4).String(), "def")
}

func TestFileTagErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")

	// missing file without default -> error
	err := parseEnv(reflect.New(structWithTags(`file:`+strconv.Quote(missing))).Interface(), map[string]string{})
	fe := isErr[fileError](t, err)
	eq(t, fe.Path, missing)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected os.ErrNotExist in chain")
	}

	// bare file, env points to a missing path -> error
	type B struct {
		A string `env:"A" file`
	}
	isErr[fileError](t, parseEnv(&B{}, map[string]string{"A": missing}))

	// a directory is not readable as a file -> error even with default
	isErr[fileError](t, parseEnv(reflect.New(structWithTags(`file:`+strconv.Quote(dir)+` $:"d"`)).Interface(), nil))

	// missing file + req -> panic
	typ := structWithTags(`env:"A" req file:` + strconv.Quote(missing))
	mustPanic(t, "A", func() { _ = parseEnv(reflect.New(typ).Interface(), map[string]string{}) })
}

type level int

type textVal struct{ s string }

func (a *textVal) UnmarshalText(b []byte) error {
	if string(b) == "bad" {
		return errors.New("bad value")
	}
	a.s = strings.ToUpper(string(b))
	return nil
}

func TestAllTypes(t *testing.T) {
	type C struct {
		S    string            `env:"S"`
		B    bool              `env:"B"`
		I    int               `env:"I"`
		I8   int8              `env:"I8"`
		I16  int16             `env:"I16"`
		I32  int32             `env:"I32"`
		I64  int64             `env:"I64"`
		U    uint              `env:"U"`
		U8   uint8             `env:"U8"`
		U16  uint16            `env:"U16"`
		U32  uint32            `env:"U32"`
		U64  uint64            `env:"U64"`
		F32  float32           `env:"F32"`
		F64  float64           `env:"F64"`
		L    level             `env:"L"`
		D    time.Duration     `env:"D"`
		PD   *time.Duration    `env:"D"`
		URL  url.URL           `env:"URL"`
		PURL *url.URL          `env:"URL"`
		Loc  time.Location     `env:"LOC"`
		T    time.Time         `env:"T"`
		TV   textVal           `env:"TV"`
		PTV  *textVal          `env:"TV"`
		PS   *string           `env:"S"`
		SS   []string          `env:"SS" div:"."`
		SI   []int             `env:"SI"`
		SP   []*int            `env:"SI"`
		STV  []textVal         `env:"STV"`
		SD   []time.Duration   `env:"SD"`
		M    map[string]int    `env:"M"`
		MKV  map[int]string    `env:"MKV" div:";" kv:"="`
		MD   map[string]level  `env:"M"`
		MP   map[string]*int   `env:"M"`
		MS   map[string]string `env:"MS"`
		Skip string            `env:"-"`
		priv string            `env:"S"` //nolint:unused
		NoT  string
	}
	env := map[string]string{
		"S": "str", "B": "true", "I": "-1", "I8": "-8", "I16": "-16", "I32": "-32", "I64": "-64",
		"U": "1", "U8": "8", "U16": "16", "U32": "32", "U64": "64",
		"F32": "1.5", "F64": "2.5", "L": "3", "D": "2s",
		"URL": "https://x.io/p?q=1", "LOC": "Europe/Berlin", "T": "2026-11-24T10:00:00Z",
		"TV": "hi", "SS": "a.b", "SI": "1,2", "STV": "x,y", "SD": "1s,2m",
		"M": "a:1,b:2", "MKV": "1=x;2=y", "MS": "k:v:w", "-": "no", "NoT": "no",
	}
	var c C
	noErr(t, parseEnv(&c, env))

	u, _ := url.Parse("https://x.io/p?q=1")
	tm, _ := time.Parse(time.RFC3339, "2026-11-24T10:00:00Z")
	d := 2 * time.Second
	s := "str"
	one, two := 1, 2

	eq(t, c.S, "str")
	eq(t, c.B, true)
	eq(t, []int64{int64(c.I), int64(c.I8), int64(c.I16), int64(c.I32), c.I64}, []int64{-1, -8, -16, -32, -64})
	eq(t, []uint64{uint64(c.U), uint64(c.U8), uint64(c.U16), uint64(c.U32), c.U64}, []uint64{1, 8, 16, 32, 64})
	eq(t, c.F32, float32(1.5))
	eq(t, c.F64, 2.5)
	eq(t, c.L, level(3))
	eq(t, c.D, d)
	eq(t, c.PD, &d)
	eq(t, c.URL, *u)
	eq(t, c.PURL, u)
	eq(t, c.Loc.String(), "Europe/Berlin")
	eq(t, c.T, tm)
	eq(t, c.TV, textVal{"HI"})
	eq(t, c.PTV, &textVal{"HI"})
	eq(t, c.PS, &s)
	eq(t, c.SS, []string{"a", "b"})
	eq(t, c.SI, []int{1, 2})
	eq(t, c.SP, []*int{&one, &two})
	eq(t, c.STV, []textVal{{"X"}, {"Y"}})
	eq(t, c.SD, []time.Duration{time.Second, 2 * time.Minute})
	eq(t, c.M, map[string]int{"a": 1, "b": 2})
	eq(t, c.MKV, map[int]string{1: "x", 2: "y"})
	eq(t, c.MD, map[string]level{"a": 1, "b": 2})
	eq(t, c.MP, map[string]*int{"a": &one, "b": &two})
	eq(t, c.MS, map[string]string{"k": "v:w"})
	eq(t, c.Skip, "")
	eq(t, c.NoT, "")
	eq(t, c.priv, "")
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		v    any
		env  string
	}{
		{"int", &struct {
			X int `env:"X"`
		}{}, "abc"},
		{"int8 overflow", &struct {
			X int8 `env:"X"`
		}{}, "300"},
		{"uint negative", &struct {
			X uint `env:"X"`
		}{}, "-1"},
		{"bool", &struct {
			X bool `env:"X"`
		}{}, "maybe"},
		{"float", &struct {
			X float64 `env:"X"`
		}{}, "x"},
		{"duration", &struct {
			X time.Duration `env:"X"`
		}{}, "1parsec"},
		{"location", &struct {
			X time.Location `env:"X"`
		}{}, "Mars/Olympus"},
		{"url", &struct {
			X url.URL `env:"X"`
		}{}, "%%"},
		{"text", &struct {
			X textVal `env:"X"`
		}{}, "bad"},
		{"slice", &struct {
			X []int `env:"X"`
		}{}, "1,x"},
		{"map format", &struct {
			X map[string]int `env:"X"`
		}{}, "a"},
		{"map value", &struct {
			X map[string]int `env:"X"`
		}{}, "a:x"},
		{"map key", &struct {
			X map[int]int `env:"X"`
		}{}, "x:1"},
		{"default", &struct {
			X int `$:"x"`
		}{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseEnv(tc.v, map[string]string{"X": tc.env})
			pe := isErr[parseError](t, err)
			eq(t, pe.Field, "X")
			if !strings.HasPrefix(err.Error(), "envy: field X") {
				t.Fatalf("bad message %q", err)
			}
		})
	}
}

func TestErrorsAggregate(t *testing.T) {
	typ := reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: reflect.TypeFor[int](), Tag: `env:"A"`},
		{Name: "B", Type: reflect.TypeFor[chan int](), Tag: `env:"B"`},
		{Name: "C", Type: reflect.TypeFor[[][]int](), Tag: `env:"C"`},
		{Name: "D", Type: reflect.TypeFor[string](), Tag: `env:"D`},
		{Name: "E", Type: reflect.TypeFor[int](), Tag: `env:"E"`},
	})
	ptr := reflect.New(typ)
	err := parseEnv(ptr.Interface(), map[string]string{"A": "x", "B": "1", "C": "1", "E": "5"})
	agg := isErr[aggregateError](t, err)
	eq(t, len(agg), 4)
	isErr[parseError](t, agg[0])
	isErr[noParserError](t, agg[1])
	isErr[noParserError](t, agg[2])
	isErr[tagError](t, agg[3])
	eq(t, ptr.Elem().Field(4).Int(), int64(5)) // valid fields still set
	if !strings.HasPrefix(err.Error(), "envy: field A") || strings.Count(err.Error(), ";") != 3 {
		t.Fatalf("bad message %q", err)
	}
}

func TestNotStructPtr(t *testing.T) {
	var s struct{}
	var nilPtr *struct{}
	for _, v := range []any{s, nilPtr, new(int), nil} {
		if err := Parse(v); !errors.Is(err, errNotStructPtr) {
			t.Fatalf("%T: got %v", v, err)
		}
	}
}

func TestDefaults(t *testing.T) {
	type C struct {
		A string `env:"A" $:"defA"`
		B string `env:"B" $:"defB"`
		C int    `default:"5"`
		D string `env:"D" $:"defD"`
		E string `env:"E" $:""`
	}
	var c C
	noErr(t, parseEnv(&c, map[string]string{"B": "envB", "D": ""}))
	eq(t, c, C{A: "defA", B: "envB", C: 5, D: "defD"})
}

func TestNested(t *testing.T) {
	type DB struct {
		Host string `env:"HOST"`
		Port int    `env:"PORT" $:"5432"`
	}
	type Embedded struct {
		E string `env:"E"`
	}
	type C struct {
		Embedded
		DB      DB  `prefix:"DB_"`
		Replica *DB `prefix:"REPLICA_"`
		NilDB   *DB `prefix:"NIL_"`
		Anon    struct {
			A     string `env:"A"`
			Inner struct {
				B string `env:"B"`
			} `prefix:"IN_"`
		} `prefix:"ANON_"`
	}
	env := map[string]string{
		"E": "e", "DB_HOST": "db", "REPLICA_HOST": "rep", "REPLICA_PORT": "1",
		"NIL_HOST": "nil", "ANON_A": "anon", "ANON_IN_B": "deep",
	}
	c := C{Replica: &DB{}}
	noErr(t, parseEnv(&c, env))
	eq(t, c.E, "e")
	eq(t, c.DB, DB{"db", 5432})
	eq(t, *c.Replica, DB{"rep", 1})
	if c.NilDB != nil {
		t.Fatal("nil ptr must stay nil")
	}
	eq(t, c.Anon.A, "anon")
	eq(t, c.Anon.Inner.B, "deep")
}

func TestSliceOfStructs(t *testing.T) {
	type Server struct {
		Host string `env:"HOST"`
		Port int    `env:"PORT" $:"80"`
	}
	type C struct {
		Servers []Server  `prefix:"SRV"`
		PS      *[]Server `prefix:"PS_"`
		Keep    []Server  `prefix:"KEEP_"`
		None    []Server  `prefix:"NONE_"`
	}
	env := map[string]string{
		"SRV_0_HOST": "a", "SRV_1_HOST": "b", "SRV_1_PORT": "81", "SRV_3_HOST": "gap",
		"SRV_00_HOST": "noncanonical",
		"PS_0_HOST":   "p",
		"KEEP_1_HOST": "k1",
	}
	c := C{Keep: []Server{{Host: "k0"}, {Host: "old"}, {Host: "k2"}}}
	noErr(t, parseEnv(&c, env))
	eq(t, c.Servers, []Server{{"a", 80}, {"b", 81}})
	eq(t, *c.PS, []Server{{"p", 80}})
	eq(t, c.Keep, []Server{{"k0", 80}, {"k1", 80}, {"k2", 80}})
	eq(t, c.None, []Server(nil))
}

func TestSliceOfStructsProcessEnv(t *testing.T) {
	t.Setenv("ENVYSRV_0_HOST", "a")
	t.Setenv("ENVYSRV_1_HOST", "b")
	type C struct {
		S []struct {
			Host string `env:"HOST"`
		} `prefix:"ENVYSRV_"`
	}
	var c C
	noErr(t, Parse(&c))
	eq(t, len(c.S), 2)
	eq(t, c.S[1].Host, "b")
}

func TestConcurrent(t *testing.T) {
	type C struct {
		A string `env:"A"`
		B int    `env:"B" $:"1"`
	}
	env := map[string]string{"A": "a"}
	done := make(chan error, 32)
	for range 32 {
		go func() {
			var c C
			err := parseEnv(&c, env)
			if err == nil && (c.A != "a" || c.B != 1) {
				err = fmt.Errorf("bad result %+v", c)
			}
			done <- err
		}()
	}
	for range 32 {
		noErr(t, <-done)
	}
}

// ---- benchmarks ----

type benchConfig struct {
	Host    string        `env:"HOST" $:"localhost"`
	Port    int           `env:"PORT" req`
	Debug   bool          `env:"DEBUG"`
	Timeout time.Duration `env:"TIMEOUT"`
	Hosts   []string      `env:"HOSTS"`
	Name    string        `!:"angels"`
	DB      struct {
		User string `env:"USER"`
		Pass string `env:"PASS"`
	} `prefix:"DB_"`
}

var benchEnv = map[string]string{
	"PORT": "8080", "DEBUG": "true", "TIMEOUT": "5s", "HOSTS": "a,b,c", "DB_USER": "u", "DB_PASS": "p",
}

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var c benchConfig
		if err := parseEnv(&c, benchEnv); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseOS(b *testing.B) {
	for k, v := range benchEnv {
		b.Setenv(k, v)
	}
	b.ReportAllocs()
	for b.Loop() {
		var c benchConfig
		if err := Parse(&c); err != nil {
			b.Fatal(err)
		}
	}
}
