package tests

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/artni96/GophProfile/internal/config"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"

	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var ErrEnvVarNotFound = errors.New("environment variable not found")

type TestDependencies struct {
	cfg *config.Config
	DB  *sqlx.DB
}

func NewTestDependencies(ctx context.Context) (*TestDependencies, error) {
	td := &TestDependencies{}
	if err := td.initConfig(); err != nil {
		return nil, err
	}
	if err := td.initDBConn(ctx); err != nil {
		return nil, err
	}
	if err := td.applyMigrations(); err != nil {
		return nil, err
	}

	return td, nil

}

func findProjectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	dir := wd
	for {
		if _, err = os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func (td *TestDependencies) initConfig() error {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}
	fmt.Println(projectRoot + "/.env-tests")
	err = godotenv.Load(projectRoot + "/.env-tests")
	if err != nil {
		return err
	}

	cfg := config.Config{}

	sslMode := "disable"

	dbHost, ok := os.LookupEnv("DB_HOST")
	if !ok {
		return ErrEnvVarNotFound
	}

	dbName, ok := os.LookupEnv("DB_NAME")
	if !ok {
		return ErrEnvVarNotFound
	}

	dbUser, ok := os.LookupEnv("DB_USER")
	if !ok {
		return ErrEnvVarNotFound
	}

	dbPassword, ok := os.LookupEnv("DB_PASSWORD")
	if !ok {
		return ErrEnvVarNotFound
	}

	dbPort, ok := os.LookupEnv("DB_PORT")
	if !ok {
		return ErrEnvVarNotFound
	}

	dbPortInt, err := strconv.Atoi(dbPort)
	if err != nil {
		return ErrEnvVarNotFound
	}

	cfg.DBDsn = fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPortInt, dbUser, dbPassword, dbName, sslMode)
	fmt.Println(cfg.DBDsn)
	td.cfg = &cfg
	return nil
}

func (td *TestDependencies) initDBConn(ctx context.Context) error {
	if td.cfg.DBDsn == "" {
		return fmt.Errorf("database dsn is not provided")
	}
	db, err := sqlx.Open("pgx", td.cfg.DBDsn)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	localCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	err = db.PingContext(localCtx)
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	td.DB = db
	return nil
}

func (c *TestDependencies) applyMigrations() error {
	driver, err := postgres.WithInstance(c.DB.DB, &postgres.Config{})
	if err != nil {
		log.Println(fmt.Errorf("failed to create database driver: %w", err))
		return err
	}

	migrator, err := migrate.NewWithDatabaseInstance("file://../../../migrations", "postgres", driver)
	if err != nil {
		log.Println(fmt.Errorf("failed to initialize test migrator: %w", err))
		return err

	}
	if err = migrator.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Println(fmt.Errorf("failed to clean up test database: %w", err))
		return err
	}
	if err = migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Println(fmt.Errorf("failed to run migrations: %w", err))
		return err
	}
	return nil
}
