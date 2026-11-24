package envy_test

import (
	"fmt"
	"os"
	"time"

	"github.com/monopolly/envy"
)

func ExampleParse() {
	os.Setenv("HTTP_HOST", "example.com")
	os.Setenv("HTTP_HOSTS", "a.com,b.com")
	os.Setenv("DB_USER", "admin")

	type DB struct {
		User string `env:"USER" req`
		Port int    `env:"PORT" $:"5432"`
	}
	type Config struct {
		Host    string        `env:"HTTP_HOST"`
		Port    int           `@:"HTTP_PORT" default:"8080"`
		Hosts   []string      `env:"HTTP_HOSTS"`
		Timeout time.Duration `env:"TIMEOUT" $:"5s"`
		Name    string        `env:"NAME" !:"angels"`
		DB      DB            `prefix:"DB_"`
	}

	var cfg Config
	if err := envy.Parse(&cfg); err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", cfg)
	// Output: {Host:example.com Port:8080 Hosts:[a.com b.com] Timeout:5s Name:angels DB:{User:admin Port:5432}}
}

func ExampleFile() {
	os.WriteFile("example.env", []byte("EXAMPLE_GREETING=hello\n"), 0o600)
	defer os.Remove("example.env")

	if err := envy.File("example.env"); err != nil {
		panic(err)
	}
	fmt.Println(os.Getenv("EXAMPLE_GREETING"))
	// Output: hello
}
