package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"antrea.io/theia/snowflake/pkg/config"
	"antrea.io/theia/snowflake/pkg/onboard"
	sf "antrea.io/theia/snowflake/pkg/snowflake"
)

var (
	appConfig config.Config
	logger    logr.Logger
)

func run(ctx context.Context) error {
	dsn, sfCfg, err := sf.GetDSN()
	if err != nil {
		return fmt.Errorf("failed to create DSN from Config: %v, err: %w", sfCfg, err)
	}

	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to %v: %w", dsn, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Using the SDK's default configuration, loading additional config
	// and credentials values from the environment variables, shared
	// credentials, and shared configuration files
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(appConfig.AWS.Region))
	if err != nil {
		return fmt.Errorf("unable to load SDK config: %w", err)
	}

	sfClient := sf.NewClient(db, logger)

	onboarder := onboard.NewOnboarder(logger, appConfig, awsCfg, sfClient)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return onboarder.Onboard(ctx)
}

func loadConfig(configPath string) error {
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}
	if err := yaml.Unmarshal(configBytes, &appConfig); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}
	if err := appConfig.ValidateAndSetDefaults(); err != nil {
		return fmt.Errorf("error when processing config: %w", err)
	}
	return nil
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to YAML config")

	flag.Parse()

	if configPath == "" {
		log.Fatalf("Config path is required")
	}

	zc := zap.NewProductionConfig()
	zc.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	zc.DisableStacktrace = true
	zapLog, err := zc.Build()
	if err != nil {
		panic("Cannot initialize Zap logger")
	}
	logger = zapr.NewLogger(zapLog)

	if err := loadConfig(configPath); err != nil {
		logger.Error(err, "Failed to load configuration")
		os.Exit(1)
	}

	if err := run(context.Background()); err != nil {
		logger.Error(err, "Error when onboarding tenant")
		os.Exit(1)
	}
}
