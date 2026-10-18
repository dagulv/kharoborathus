package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProcessEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	file := "POSTGRES_PASSWORD=from-file\nPOSTGRES_DB=from-file\nPOSTGRES_CONNECTION_STRING=postgres://u:${POSTGRES_PASSWORD}@db/${POSTGRES_DB}\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("POSTGRES_PASSWORD", "from-process")

	e, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}

	if e.PostgresPassword != "from-process" {
		t.Errorf("PostgresPassword = %q, want from-process", e.PostgresPassword)
	}
	if e.PostgresDb != "from-file" {
		t.Errorf("PostgresDb = %q, want from-file", e.PostgresDb)
	}
	if want := "postgres://u:from-process@db/from-file"; e.PostgresConn != want {
		t.Errorf("PostgresConn = %q, want %q", e.PostgresConn, want)
	}
}

func TestLoadFilePrecedence(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"infra/db/.env":  "POSTGRES_USER=from-db\nPOSTGRES_DB=from-db\nPOSTGRES_PASSWORD=from-db\n",
		"infra/api/.env": "POSTGRES_DB=from-api\nPOSTGRES_PASSWORD=from-api\n",
		".env":           "POSTGRES_PASSWORD=from-root\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	e, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}

	if e.PostgresUser != "from-db" {
		t.Errorf("PostgresUser = %q, want from-db", e.PostgresUser)
	}
	if e.PostgresDb != "from-api" {
		t.Errorf("PostgresDb = %q, want from-api", e.PostgresDb)
	}
	if e.PostgresPassword != "from-root" {
		t.Errorf("PostgresPassword = %q, want from-root", e.PostgresPassword)
	}
}

func TestLoadDefaultConnection(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("POSTGRES_PASSWORD=pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	e, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}

	if want := "postgresql://postgres:pw@localhost:5432/postgres?sslmode=disable"; e.PostgresConn != want {
		t.Errorf("PostgresConn = %q, want %q", e.PostgresConn, want)
	}
}
