<p align="center">
  <img src="logo.png" alt="envy" width="360">
</p>

<p align="center">
  Environment variables → Go structs. Two functions, short tags, zero dependencies.
</p>

<p align="center">
  <a href="https://pkg.go.dev/github.com/monopolly/envy"><img src="https://pkg.go.dev/badge/github.com/monopolly/envy.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/monopolly/envy"><img src="https://goreportcard.com/badge/github.com/monopolly/envy" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT"></a>
</p>

---

```go
type Config struct {
	Host  string   `env:"HTTP_HOST" $:"localhost"`
	Port  int      `env:"HTTP_PORT" req`
	Name  string   `!:"angels"`
	Hosts []string `env:"HTTP_HOSTS"`
}

envy.File(".env.dev")

var cfg Config
if err := envy.Parse(&cfg); err != nil {
	log.Fatal(err)
}
```

## Install

```sh
go get github.com/monopolly/envy
```

Requires Go 1.26+.

## API

The entire API is two functions:

```go
func Parse(v any) error      // fill a struct: envy.Parse(&cfg)
func File(name string) error // load a .env file: envy.File(".env.dev")
```

`Parse` returns an error describing **every** field that failed to parse, not just the first one.
It **panics** if a field marked `req` has no value, so you can't start with a broken config.

## Tags

| Tag | Alias | Meaning |
|---|---|---|
| `env:"KEY"` | `@:"KEY"` | Variable name. `env:"-"` skips the field. |
| `$:"value"` | `default:"value"` | Used when the variable is unset or empty. |
| `!:"value"` | | Constant. Always wins over env, file and default. |
| `req` | `req:""` | Panic if the field ends up without a value. |
| `file:"path"` | | Read the value from a file when the variable is unset. |
| `file` | | The variable holds a file path; read the file's contents. |
| `prefix:"DB_"` | | Key prefix for a nested struct or `[]struct`. |
| `div:","` | | Item separator for slices and maps. Default `,`. |
| `kv:":"` | | Key/value separator for maps. Default `:`. |

A value is resolved in this order, first match wins:

```
!  →  env  →  file  →  default
```

### Constants

`!` pins a value in code. Nothing in the environment can override it, which is handy for
fields that must never change between deployments:

```go
Name string `env:"NAME" !:"angels"` // always "angels", even if NAME is set
```

### Required fields

```go
Port int    `env:"PORT" req`
Key  string `env:"KEY" req $:"dev-key"` // a default satisfies req
```

An empty variable counts as unset. All missing keys are reported in one panic:

```
panic: envy: required values not set: PORT, DB_HOST
```

> `go vet` flags bare tags like `req` and `file` (`bad syntax for struct tag pair`).
> They work fine with envy. If you want vet to stay quiet, write `req:""`, or run
> `go vet -structtag=false`.

### Secrets from files

```go
// Use PRIVATE_KEY if set, otherwise read ./private.key
Key string `env:"PRIVATE_KEY" file:"private.key"`

// TLS_CERT contains a path, e.g. /run/secrets/cert.pem; the field gets the file's contents
Cert string `env:"TLS_CERT" file`
```

A missing file falls back to the default (or triggers the `req` panic). With neither, `Parse` returns an error.

## Nested structs

```go
type DB struct {
	Host string `env:"HOST" $:"localhost"`
	Port int    `env:"PORT" $:"5432"`
}

type Config struct {
	DB      DB  `prefix:"DB_"`      // DB_HOST, DB_PORT
	Replica *DB `prefix:"REPLICA_"` // filled only if non-nil
	Cache   struct {
		TTL time.Duration `env:"TTL"`
	} `prefix:"CACHE_"`             // CACHE_TTL
}
```

Prefixes stack: a struct with `prefix:"IN_"` inside `prefix:"APP_"` reads `APP_IN_*`.
Embedded structs are read without a prefix.

## Slices of structs

Indexed variables become slice elements:

```go
type Server struct {
	Host string `env:"HOST"`
	Port int    `env:"PORT" $:"80"`
}

type Config struct {
	Servers []Server `prefix:"SERVERS"`
}
```

```sh
SERVERS_0_HOST=a.example.com
SERVERS_1_HOST=b.example.com
SERVERS_1_PORT=8080
```

```go
[]Server{{"a.example.com", 80}, {"b.example.com", 8080}}
```

Indices must be contiguous from `0`; reading stops at the first gap.

## Supported types

- `string`, `bool`, all `int*`, `uint*` and `float*` types, and named types based on them
- `time.Duration`, `time.Location`, `url.URL`
- anything implementing `encoding.TextUnmarshaler` (`time.Time`, `netip.Addr`, `slog.Level`, your own types)
- pointers to all of the above (allocated on demand)
- slices of the above: `HOSTS=a,b,c` → `[]string{"a", "b", "c"}`
- maps of the above: `LIMITS=read:10,write:5` → `map[string]int{"read": 10, "write": 5}`

Whitespace around slice and map items is trimmed: `a, b` and `a,b` give the same result.

```go
Hosts  []string          `env:"HOSTS"`                 // a,b,c
Ports  []int             `env:"PORTS" div:";"`         // 80;443
Limits map[string]int    `env:"LIMITS" div:";" kv:"="` // read=10;write=5
Level  slog.Level        `env:"LOG_LEVEL" $:"info"`
Start  time.Time         `env:"START"`                 // RFC 3339
TZ     time.Location     `env:"TZ" $:"UTC"`
API    *url.URL          `env:"API_URL"`
```

## .env files

`File` loads variables into the process environment. **Variables that are already set are
kept**, so real environment variables always take precedence over the file. Call it several
times to layer files; the first file to set a key wins:

```go
envy.File(".env.local") // personal overrides
envy.File(".env")       // shared defaults
```

`envy.File("")` loads `.env`. A missing file returns an error that matches `os.ErrNotExist`,
so you can make the file optional:

```go
if err := envy.File(".env"); err != nil && !errors.Is(err, fs.ErrNotExist) {
	log.Fatal(err)
}
```

Supported syntax:

```sh
# comments and blank lines
HOST=localhost
export PORT=8080              # "export" is optional
NAME=some value # comment     # unquoted: trimmed, " #" starts a comment
URL=http://x.io/#anchor       # "#" without a preceding space is kept

GREETING="hello\nworld"       # double quotes: \n \t \" \\ \$ escapes
ADDR="${HOST}:$PORT"          # $VAR and ${VAR} expansion (unquoted and double-quoted)
RAW='no $expansion \n here'   # single quotes: literal

CERT="-----BEGIN CERT-----
MIIB...
-----END CERT-----"           # quoted values may span lines
```

Syntax errors report the file and line: `envy: .env.dev:12: unterminated quoted value`.

## Performance

Struct tags are parsed once per type and cached, and values are set without going through
`interface{}`. A typical 8-field config with a nested struct:

```
BenchmarkParse     ~0.7 µs/op    248 B/op    6 allocs/op
```

`Parse` is safe to call concurrently.

## Credits

envy started as a fork of [caarlos0/env](https://github.com/caarlos0/env) and was then rewritten
with a smaller API and shorter tags.

## License

[MIT](LICENSE)
