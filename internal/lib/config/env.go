package config

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v11"
)

type Environment struct {
	AppUrl           string `env:"APP_URL" envDefault:"http://localhost:3000"`
	PostgresUser     string `env:"POSTGRES_USER" envDefault:"postgres"`
	PostgresPassword string `env:"POSTGRES_PASSWORD" envDefault:"postgres"`
	PostgresDb       string `env:"POSTGRES_DB" envDefault:"postgres"`
	PostgresConn     string `env:"POSTGRES_CONNECTION_STRING,expand" envDefault:"postgresql://${POSTGRES_USER}:${POSTGRES_PASSWORD}@${POSTGRES_HOST}/${POSTGRES_NAME}?sslmode=disable"`
	HttpHost         string `env:"HTTP_HOST" envDefault:"0.0.0.0:3001"`
	HttpCors         string `env:"HTTP_CORS" envDefault:"*"`
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
	lines := bufio.NewScanner(f)

	for lines.Scan() {
		rec := bytes.SplitN(lines.Bytes(), []byte{'='}, 2)

		if len(rec) == 2 {
			m[string(rec[0])] = string(rec[1])
		}
	}

	return m, lines.Err()
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
