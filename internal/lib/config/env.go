package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/caarlos0/env/v11"
)

type Environment struct {
	PostgresUser     string `env:"POSTGRES_USER" envDefault:"postgres"`
	PostgresPassword string `env:"POSTGRES_PASSWORD" envDefault:"postgres"`
	PostgresDb       string `env:"POSTGRES_DB" envDefault:"postgres"`
	PostgresConn     string `env:"POSTGRES_CONNECTION_STRING,expand" envDefault:"postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@localhost:5432/${POSTGRES_DB}?sslmode=disable"`
	HttpHost         string `env:"HTTP_HOST" envDefault:"0.0.0.0:3001"`
	InternalHttpHost string `env:"INTERNAL_HTTP_HOST" envDefault:"0.0.0.0:3002"`
	DeepseekAPIKey   string `env:"DEEPSEEK_API_KEY" envDefault:""`
	TypesafeAPIKey   string `env:"TYPESAFE_API_KEY" envDefault:""`
	BraveAPIKey      string `env:"BRAVE_API_KEY" envDefault:""`
}

func Load(useFile ...bool) (e *Environment, err error) {
	var m map[string]string

	if len(useFile) == 0 {
		_, f := os.LookupEnv("USE_ENV_FILE")
		useFile = append(useFile, f)
	}

	if len(useFile) > 0 && useFile[0] {
		// If file doesn't exist, use OS environment variables
		m, _ = fromFile()
	}

	// The process env wins, so infra/secrets.sh overrides both files.
	if m != nil {
		for _, kv := range os.Environ() {
			k, v, _ := strings.Cut(kv, "=")
			m[k] = v
		}
	}

	e = new(Environment)

	if err = env.ParseWithOptions(e, env.Options{
		Environment:     m,
		RequiredIfNoDef: true,
	}); err != nil {
		return
	}

	return
}

func fromFile() (m map[string]string, err error) {
	f, err := findFile()

	if err != nil {
		return
	}

	defer f.Close()

	m = make(map[string]string)

	// The deploy's safe values, beside the root .env. Read first so the root .env overrides them in dev.
	api, err := os.Open(filepath.Join(filepath.Dir(f.Name()), "infra", "api", ".env"))
	if err == nil {
		defer api.Close()

		if err = parse(api, m); err != nil {
			return nil, fmt.Errorf("err read infra/api/.env: %w", err)
		}
	}

	if err = parse(f, m); err != nil {
		return nil, fmt.Errorf("err read .env: %w", err)
	}

	return m, nil
}

func parse(r io.Reader, m map[string]string) error {
	lines := bufio.NewScanner(r)

	for lines.Scan() {
		rec := bytes.SplitN(lines.Bytes(), []byte{'='}, 2)

		if len(rec) == 2 {
			m[string(rec[0])] = string(rec[1])
		}
	}

	return lines.Err()
}

func findFile() (f *os.File, err error) {
	dir, err := os.Getwd()

	if err != nil {
		return
	}

	for dir != "." && dir != "/" {
		if f, err = os.Open(filepath.Join(dir, ".env")); err == nil {
			return
		}

		dir = filepath.Dir(dir)
	}

	return nil, errors.New("no .env file found")
}
