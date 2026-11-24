package envy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadDotenv(t *testing.T) {
	t.Setenv("ENVY_OUTER", "outer")
	src := "\uFEFF" + `# comment
EMPTY=
PLAIN=value
SPACED =  some value
export EXPORTED=yes
INLINE=abc # comment
HASH=abc#notcomment
DQ="hello \"world\"\n\tend"
SQ='literal $PLAIN \n'
REF=${PLAIN}-$PLAIN-$ENVY_OUTER-$MISSING
DQREF="${PLAIN} \$PLAIN"
MULTI="line1
line2"
SQMULTI='a
b' # trailing comment
DOTTED.KEY-1=ok
CRLF=win` + "\r\n" + `DOLLAR=cost $ 5 $
UNCLOSED_BRACE=${abc
`
	m, err := parseDotenv(src)
	noErr(t, err)
	eq(t, m, map[string]string{
		"EMPTY":          "",
		"PLAIN":          "value",
		"SPACED":         "some value",
		"EXPORTED":       "yes",
		"INLINE":         "abc",
		"HASH":           "abc#notcomment",
		"DQ":             "hello \"world\"\n\tend",
		"SQ":             `literal $PLAIN \n`,
		"REF":            "value-value-outer-",
		"DQREF":          "value $PLAIN",
		"MULTI":          "line1\nline2",
		"SQMULTI":        "a\nb",
		"DOTTED.KEY-1":   "ok",
		"CRLF":           "win",
		"DOLLAR":         "cost $ 5 $",
		"UNCLOSED_BRACE": "${abc",
	})
}

func TestReadDotenvErrors(t *testing.T) {
	cases := map[string]struct {
		src  string
		line int
	}{
		"no equals":    {"A=1\nJUSTKEY\n", 2},
		"bad key":      {"BAD KEY=1", 1},
		"empty key":    {"=1", 1},
		"unterminated": {"A=1\nB=\"abc\nC=2\n", 2},
		"garbage":      {`A="x" y`, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseDotenv(tc.src)
			fe := isErr[dotenvError](t, err)
			eq(t, fe.Line, tc.line)
		})
	}
}

func TestFile(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.env")
	f2 := filepath.Join(dir, "b.env")
	noErr(t, os.WriteFile(f1, []byte("ENVY_F_HOST=file-host\nENVY_F_PORT=9000\nENVY_F_KEEP=file\n"), 0o600))
	noErr(t, os.WriteFile(f2, []byte("ENVY_F_PORT=1\nENVY_F_EXTRA=extra\n"), 0o600))

	t.Setenv("ENVY_F_KEEP", "real")
	for _, k := range []string{"ENVY_F_HOST", "ENVY_F_PORT", "ENVY_F_EXTRA"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	noErr(t, File(f1))
	noErr(t, File(f2))

	type C struct {
		Host  string `env:"ENVY_F_HOST"`
		Port  int    `env:"ENVY_F_PORT"`
		Keep  string `env:"ENVY_F_KEEP"`
		Extra string `env:"ENVY_F_EXTRA"`
		Name  string `env:"ENVY_F_HOST" !:"angels"`
	}
	var c C
	noErr(t, Parse(&c))
	eq(t, c, C{Host: "file-host", Port: 9000, Keep: "real", Extra: "extra", Name: "angels"})
}

func TestFileDefaultName(t *testing.T) {
	dir := t.TempDir()
	noErr(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("ENVY_DEFAULT_FILE=1"), 0o600))
	t.Chdir(dir)
	t.Setenv("ENVY_DEFAULT_FILE", "")
	os.Unsetenv("ENVY_DEFAULT_FILE")

	noErr(t, File(""))
	eq(t, os.Getenv("ENVY_DEFAULT_FILE"), "1")
}

func TestFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := File(filepath.Join(dir, "missing.env")); !os.IsNotExist(err) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
	bad := filepath.Join(dir, "bad.env")
	noErr(t, os.WriteFile(bad, []byte("OK=1\nbroken"), 0o600))
	fe := isErr[dotenvError](t, File(bad))
	eq(t, fe.Name, bad)
	eq(t, fe.Line, 2)
	if !strings.Contains(fe.Error(), "bad.env:2") {
		t.Fatalf("bad message %q", fe.Error())
	}
}

func TestReadDotenvAsEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env.dev")
	noErr(t, os.WriteFile(path, []byte("http_host=h\nhttp_port=1\nhttp_hosts=x,y\n"), 0o600))
	m, err := readDotenv(path)
	noErr(t, err)
	var c Config
	noErr(t, parseEnv(&c, m))
	eq(t, c, Config{Host: "h", Port: 1, Name: "angels", Hosts: []string{"x", "y"}})
}
