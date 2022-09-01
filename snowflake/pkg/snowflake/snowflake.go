package snowflake

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/go-logr/logr"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/snowflake"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

const (
	schemaName         = "theia"
	udfStageName       = "udfs"
	ingestionStageName = "flowstage"
	ingestionPipeName  = "flowpipe"
)

type Client interface {
	CreateDatabase(ctx context.Context, dbName string) error
	UseDatabase(ctx context.Context, dbName string) error
	DeleteDatabase(ctx context.Context, dbName string) error

	CreateSchema(ctx context.Context, schemaName string) error
	UseSchema(ctx context.Context, schemaName string) error

	// not db scoped
	CreateStorageIntegration(ctx context.Context, storageIntegrationName string, awsRoleARN string, s3Path string) (string, string, error)

	CreateIngestionStage(ctx context.Context, stageName string, s3Path string, storageIntegrationName string) error
	CreateAutoIngestPipe(ctx context.Context, pipeName string, tableName string, stageName string, errorIntegrationName string) (string, error)

	CreateUDFStage(ctx context.Context, stageName string) error

	CreateNotificationIntegration(ctx context.Context, notificationIntegrationName string, awsSNSTopicARN string, awsRoleARN string) (string, string, error)

	RunMigrations(ctx context.Context, fsys fs.FS, migrationsPath string) error

	CreateFlowDeletionTask(ctx context.Context, taskName string, initialWarehouseSize string, flowRetentionDays int) error
	DeleteFlowDeletionTask(ctx context.Context, taskName string) error
}

type client struct {
	db     *sql.DB
	logger logr.Logger
}

func NewClient(db *sql.DB, logger logr.Logger) *client {
	return &client{
		db:     db,
		logger: logger,
	}
}

func (c *client) CreateDatabase(ctx context.Context, dbName string) error {
	query := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", dbName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) UseDatabase(ctx context.Context, dbName string) error {
	query := fmt.Sprintf("USE DATABASE %s", dbName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) DeleteDatabase(ctx context.Context, dbName string) error {
	query := fmt.Sprintf("DROP DATABASE IF EXISTS %s", dbName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) CreateSchema(ctx context.Context, schemaName string) error {
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) UseSchema(ctx context.Context, schemaName string) error {
	query := fmt.Sprintf("USE SCHEMA %s", schemaName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) CreateStorageIntegration(
	ctx context.Context,
	storageIntegrationName string,
	awsRoleARN string,
	s3Path string,
) (string, string, error) {
	query := fmt.Sprintf(`CREATE OR REPLACE STORAGE INTEGRATION %s
  TYPE = EXTERNAL_STAGE
  STORAGE_PROVIDER = S3
  ENABLED = TRUE
  STORAGE_AWS_ROLE_ARN = '%s'
  STORAGE_ALLOWED_LOCATIONS = ('%s')`, storageIntegrationName, awsRoleARN, s3Path)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("error when configuring storage integration: %w", err)
	}

	query = fmt.Sprintf("desc integration %s", storageIntegrationName)

	c.logger.Info("Executing Snowflake query", "query", query)
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("error when describing storage integration: %w", err)
	}
	var iamUserARN, externalID string
	for rows.Next() {
		var property, propertyType, propertyValue, propertyDefault string
		if err := rows.Scan(&property, &propertyType, &propertyValue, &propertyDefault); err != nil {
			return "", "", fmt.Errorf("error when scanning row: %w", err)
		}
		switch property {
		case "STORAGE_AWS_IAM_USER_ARN":
			iamUserARN = propertyValue
		case "STORAGE_AWS_EXTERNAL_ID":
			externalID = propertyValue
		default:
			continue
		}
	}
	if iamUserARN == "" || externalID == "" {
		return "", "", fmt.Errorf("missing property")
	}

	return iamUserARN, externalID, nil
}

func (c *client) CreateIngestionStage(
	ctx context.Context,
	stageName string,
	s3Path string,
	storageIntegrationName string) error {
	query := fmt.Sprintf(`CREATE STAGE IF NOT EXISTS %s
  URL = '%s'
  STORAGE_INTEGRATION = %s
`, stageName, s3Path, storageIntegrationName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) CreateAutoIngestPipe(
	ctx context.Context,
	pipeName string,
	tableName string,
	stageName string,
	errorIntegrationName string,
) (string, error) {
	query := fmt.Sprintf("CREATE PIPE IF NOT EXISTS %s AUTO_INGEST = true", pipeName)
	if errorIntegrationName != "" {
		query += " ERROR_INTEGRATION = " + errorIntegrationName
	}
	query += fmt.Sprintf(`
AS
  COPY INTO %s
  FROM @%s
  FILE_FORMAT = (TYPE = 'CSV')`, tableName, stageName)
	c.logger.Info("Snowflake query", "query", query)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return "", err
	}

	query = fmt.Sprintf("DESCRIBE PIPE %s", pipeName)
	c.logger.Info("Snowflake query", "query", query)
	row := c.db.QueryRowContext(ctx, query)
	var (
		createdOn           sql.NullString
		name                sql.NullString
		databaseName        sql.NullString
		schemaName          sql.NullString
		definition          sql.NullString
		owner               sql.NullString
		notificationChannel sql.NullString
		comment             sql.NullString
		integration         sql.NullString
		pattern             sql.NullString
		errorIntegration    sql.NullString
	)
	if err := row.Scan(
		&createdOn,
		&name,
		&databaseName,
		&schemaName,
		&definition,
		&owner,
		&notificationChannel,
		&comment,
		&integration,
		&pattern,
		&errorIntegration,
	); err != nil {
		return "", err
	}
	if !notificationChannel.Valid {
		return "", fmt.Errorf("no notification channel")
	}
	return notificationChannel.String, nil
}

func (c *client) CreateUDFStage(ctx context.Context, stageName string) error {
	query := fmt.Sprintf("CREATE STAGE IF NOT EXISTS %s", stageName)
	c.logger.Info("Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	return err
}

func (c *client) CreateNotificationIntegration(
	ctx context.Context,
	notificationIntegrationName string,
	awsSNSTopicARN string,
	awsRoleARN string,
) (string, string, error) {
	query := fmt.Sprintf(`
CREATE OR REPLACE NOTIFICATION INTEGRATION %s
  TYPE = QUEUE
  NOTIFICATION_PROVIDER = AWS_SNS
  ENABLED = TRUE
  DIRECTION = OUTBOUND
  AWS_SNS_TOPIC_ARN = '%s'
  AWS_SNS_ROLE_ARN = '%s'`, notificationIntegrationName, awsSNSTopicARN, awsRoleARN)

	c.logger.Info("Executing Snowflake query", "query", query)
	_, err := c.db.ExecContext(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("error when configuring notification integration: %w", err)
	}

	query = fmt.Sprintf("desc notification integration %s", notificationIntegrationName)

	c.logger.Info("Executing Snowflake query", "query", query)
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return "", "", fmt.Errorf("error when describing notification integration: %w", err)
	}
	var iamUserARN, externalID string
	for rows.Next() {
		var property, propertyType, propertyValue, propertyDefault string
		if err := rows.Scan(&property, &propertyType, &propertyValue, &propertyDefault); err != nil {
			return "", "", fmt.Errorf("error when scanning row: %w", err)
		}
		switch property {
		case "SF_AWS_IAM_USER_ARN":
			iamUserARN = propertyValue
		case "SF_AWS_EXTERNAL_ID":
			externalID = propertyValue
		default:
			continue
		}
	}
	if iamUserARN == "" || externalID == "" {
		return "", "", fmt.Errorf("missing property")
	}

	return iamUserARN, externalID, nil
}

func (c *client) RunMigrations(ctx context.Context, fsys fs.FS, migrationsPath string) error {
	sourceInstance, err := iofs.New(fsys, migrationsPath)
	if err != nil {
		return err
	}
	dbInstance, err := snowflake.WithInstance(c.db, &snowflake.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", sourceInstance, "snowflake", dbInstance)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil {
		return fmt.Errorf("error when running migrations: %w", err)
	}
	return nil
}

func (c *client) CreateFlowDeletionTask(
	ctx context.Context,
	taskName string,
	initialWarehouseSize string,
	flowRetentionDays int,
) error {
	query := fmt.Sprintf(`CREATE OR REPLACE TASK %s
  SCHEDULE = 'USING CRON 0 0 * * * UTC'
  USER_TASK_MANAGED_INITIAL_WAREHOUSE_SIZE = '%s'
AS
  DELETE FROM flows WHERE DATEDIFF(day, timeInserted, CURRENT_TIMESTAMP) > %d`, taskName, initialWarehouseSize, flowRetentionDays)

	c.logger.Info("Creating flow deletion task", "query", query)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("error when creating flow deletion task: %w", err)
	}

	// By default tasks are suspended and need to be resumed
	query = fmt.Sprintf(`ALTER TASK %s RESUME`, taskName)

	c.logger.Info("Resuming flow deletion task", "query", query)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("error when resuming flow deletion task: %w", err)
	}

	return nil
}

func (c *client) DeleteFlowDeletionTask(
	ctx context.Context,
	taskName string,
) error {
	query := fmt.Sprintf(`DROP TASK IF EXISTS %s`, taskName)

	c.logger.Info("Deleting flow deletion task", "query", query)
	if _, err := c.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("error when creating flow deletion task: %w", err)
	}

	return nil
}
