package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestMigrateSkipsWithoutDatabaseURL(t *testing.T) {
	if os.Getenv("DATABASE_URL_TEST") != "" {
		t.Skip("DATABASE_URL_TEST is set; full integration covered separately")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := Migrate(ctx, nil)
	if err == nil {
		t.Fatal("expected error when pool is nil")
	}
}

func TestLoadMigrationsFindsInit(t *testing.T) {
	files, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one migration")
	}
	if files[0].version != 1 {
		t.Errorf("first version = %d, want 1", files[0].version)
	}
	if files[0].sql == "" {
		t.Error("migration SQL is empty")
	}
}
