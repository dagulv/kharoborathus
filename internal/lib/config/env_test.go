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

func TestLoadRootFileOverridesAPIFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "infra", "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "infra", "api", ".env"), []byte("POSTGRES_USER=from-api\nPOSTGRES_DB=from-api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("POSTGRES_DB=from-root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	e, err := Load(true)
	if err != nil {
		t.Fatal(err)
	}

	if e.PostgresUser != "from-api" {
		t.Errorf("PostgresUser = %q, want from-api", e.PostgresUser)
	}
	if e.PostgresDb != "from-root" {
		t.Errorf("PostgresDb = %q, want from-root", e.PostgresDb)
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
