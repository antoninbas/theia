package stack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/go-logr/logr"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/snowflake"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

type DBMigrator interface {
	DBMigrate(ctx context.Context, fsys fs.FS, migrationsPath string) error
}

type dbMigrator struct {
	db     *sql.DB
	logger logr.Logger
}

func NewDBMigrator(db *sql.DB, logger logr.Logger) *dbMigrator {
	return &dbMigrator{
		db:     db,
		logger: logger,
	}
}

func (dbm *dbMigrator) DBMigrate(ctx context.Context, fsys fs.FS, migrationsPath string) error {
	sourceInstance, err := iofs.New(fsys, migrationsPath)
	if err != nil {
		return err
	}
	dbInstance, err := snowflake.WithInstance(dbm.db, &snowflake.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", sourceInstance, "snowflake", dbInstance)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			dbm.logger.Info("Database is already up-to-date")
			return nil
		}
		return fmt.Errorf("error when running migrations: %w", err)
	}
	return nil
}
