package stack

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-logr/logr"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/s3"
	"github.com/pulumi/pulumi-snowflake/sdk/go/snowflake"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const (
	projectName = "antrea"
	stackName   = "theia-infra"

	pulumiVersion                = "v3.39.1"
	pulumiAWSPluginVersion       = "v5.13.0"
	pulumiSnowflakePluginVersion = "v0.13.0"

	flowRecordsRetentionDays    = 7
	s3BucketPrefix              = "antrea-flows"
	storageIAMRoleName          = "antrea-sf-storage-iam-role"
	storageIAMPolicyName        = "antrea-sf-storage-iam-policy"
	storageIntegrationName      = "ANTONIN_TEST_STORAGE_INTEGRATION"
	notificationIAMRoleName     = "antrea-sf-notification-iam-role"
	notificationIAMPolicyName   = "antrea-sf-notification-iam-policy"
	notificationIntegrationName = "ANTONIN_TEST_NOTIFICATION_INTEGRATION"
	databaseName                = "ANTONIN_TEST_ANTREA"
	schemaName                  = "THEIA"
	flowRetentionDays           = 30
	flowDeletionTaskName        = "DELETE_STALE_FLOWS"
	udfStageName                = "UDFS"
	ingestionStageName          = "FLOWSTAGE"
)

func declareSnowflakeIngestion(bucketName string, accountID string) func(ctx *pulumi.Context) (*snowflake.StorageIntegration, *iam.Role, error) {
	declareFunc := func(ctx *pulumi.Context) (*snowflake.StorageIntegration, *iam.Role, error) {
		storageIntegration, err := snowflake.NewStorageIntegration(ctx, "antrea-sf-storage-integration", &snowflake.StorageIntegrationArgs{
			Name:                    pulumi.String(storageIntegrationName),
			Type:                    pulumi.String("EXTERNAL_STAGE"),
			Enabled:                 pulumi.Bool(true),
			StorageAllowedLocations: pulumi.ToStringArray([]string{fmt.Sprintf("s3://%s/%s/", bucketName, s3BucketPrefix)}),
			StorageProvider:         pulumi.String("S3"),
			StorageAwsRoleArn:       pulumi.Sprintf("arn:aws:iam::%s:role/%s", accountID, pulumi.String(storageIAMRoleName)),
		})
		if err != nil {
			return nil, nil, err
		}

		storagePolicyDocument, err := iam.GetPolicyDocument(ctx, &iam.GetPolicyDocumentArgs{
			Statements: []iam.GetPolicyDocumentStatement{
				iam.GetPolicyDocumentStatement{
					Sid:    pulumi.StringRef("1"),
					Effect: pulumi.StringRef("Allow"),
					Actions: []string{
						"s3:GetObject",
						"s3:GetObjectVersion",
					},
					Resources: []string{
						fmt.Sprintf("arn:aws:s3:::%s/%s/*", bucketName, s3BucketPrefix),
					},
				},
				iam.GetPolicyDocumentStatement{
					Sid:    pulumi.StringRef("2"),
					Effect: pulumi.StringRef("Allow"),
					Actions: []string{
						"s3:ListBucket",
					},
					Resources: []string{
						fmt.Sprintf("arn:aws:s3:::%s", bucketName),
					},
					Conditions: []iam.GetPolicyDocumentStatementCondition{
						iam.GetPolicyDocumentStatementCondition{
							Test:     "StringLike",
							Variable: "s3:prefix",
							Values:   []string{fmt.Sprintf("%s/*", s3BucketPrefix)},
						},
					},
				},
				iam.GetPolicyDocumentStatement{
					Sid:    pulumi.StringRef("3"),
					Effect: pulumi.StringRef("Allow"),
					Actions: []string{
						"s3:GetBucketLocation",
					},
					Resources: []string{
						fmt.Sprintf("arn:aws:s3:::%s", bucketName),
					},
				},
			},
		})
		if err != nil {
			return nil, nil, err
		}

		// For some reason pulumi reports a diff on every refresh, when in fact the resource
		// does not need to be updated and is not actually updated during the "up" stage.
		// See https://github.com/pulumi/pulumi-aws/issues/2024
		storageIAMPolicy, err := iam.NewPolicy(ctx, "antrea-sf-storage-iam-policy", &iam.PolicyArgs{
			Name:   pulumi.String(storageIAMPolicyName),
			Policy: pulumi.String(storagePolicyDocument.Json),
		})
		if err != nil {
			return nil, nil, err
		}

		storageAssumeRolePolicyDocument := iam.GetPolicyDocumentOutput(ctx, iam.GetPolicyDocumentOutputArgs{
			Statements: iam.GetPolicyDocumentStatementArray{
				iam.GetPolicyDocumentStatementArgs{
					Sid:     pulumi.String("1"),
					Actions: pulumi.ToStringArray([]string{"sts:AssumeRole"}),
					Principals: iam.GetPolicyDocumentStatementPrincipalArray{
						iam.GetPolicyDocumentStatementPrincipalArgs{
							Type:        pulumi.String("AWS"),
							Identifiers: pulumi.StringArray([]pulumi.StringInput{storageIntegration.StorageAwsIamUserArn}),
						},
					},
					Conditions: iam.GetPolicyDocumentStatementConditionArray{
						iam.GetPolicyDocumentStatementConditionArgs{
							Test:     pulumi.String("StringEquals"),
							Variable: pulumi.String("sts:ExternalId"),
							Values:   pulumi.StringArray([]pulumi.StringInput{storageIntegration.StorageAwsExternalId}),
						},
					},
				},
			},
		})

		storageIAMRole, err := iam.NewRole(ctx, "antrea-sf-storage-iam-policy", &iam.RoleArgs{
			Name:             pulumi.String(storageIAMRoleName),
			AssumeRolePolicy: storageAssumeRolePolicyDocument.Json(),
		})
		if err != nil {
			return nil, nil, err
		}

		_, err = iam.NewRolePolicyAttachment(ctx, "antrea-sf-storage-iam-role-policy-attachment", &iam.RolePolicyAttachmentArgs{
			Role:      storageIAMRole.Name,
			PolicyArn: storageIAMPolicy.Arn,
		})
		if err != nil {
			return nil, nil, err
		}

		return storageIntegration, storageIAMRole, nil
	}
	return declareFunc
}

func declareSnowflakeErrorNotifications(snsTopicARN string, accountID string) func(ctx *pulumi.Context) (*snowflake.NotificationIntegration, *iam.Role, error) {
	declareFunc := func(ctx *pulumi.Context) (*snowflake.NotificationIntegration, *iam.Role, error) {
		if snsTopicARN == "" {
			return nil, nil, nil
		}

		notificationIntegration, err := snowflake.NewNotificationIntegration(ctx, "antrea-sf-notification-integration", &snowflake.NotificationIntegrationArgs{
			Name:                 pulumi.String(notificationIntegrationName),
			AwsSnsRoleArn:        pulumi.Sprintf("arn:aws:iam::%s:role/%s", accountID, pulumi.String(notificationIAMRoleName)),
			AwsSnsTopicArn:       pulumi.String(snsTopicARN),
			Direction:            pulumi.String("OUTBOUND"),
			Enabled:              pulumi.Bool(true),
			NotificationProvider: pulumi.String("AWS_SNS"),
			Type:                 pulumi.String("QUEUE"),
		})
		if err != nil {
			return nil, nil, err
		}

		notificationPolicyDocument, err := iam.GetPolicyDocument(ctx, &iam.GetPolicyDocumentArgs{
			Statements: []iam.GetPolicyDocumentStatement{
				iam.GetPolicyDocumentStatement{
					Sid:    pulumi.StringRef("1"),
					Effect: pulumi.StringRef("Allow"),
					Actions: []string{
						"sns:Publish",
					},
					Resources: []string{
						snsTopicARN,
					},
				},
			},
		})
		if err != nil {
			return nil, nil, err
		}

		notificationIAMPolicy, err := iam.NewPolicy(ctx, "antrea-sf-notification-iam-policy", &iam.PolicyArgs{
			Name:   pulumi.String(notificationIAMPolicyName),
			Policy: pulumi.String(notificationPolicyDocument.Json),
		})
		if err != nil {
			return nil, nil, err
		}

		notificationAssumeRolePolicyDocument := iam.GetPolicyDocumentOutput(ctx, iam.GetPolicyDocumentOutputArgs{
			Statements: iam.GetPolicyDocumentStatementArray{
				iam.GetPolicyDocumentStatementArgs{
					Sid:     pulumi.String("1"),
					Actions: pulumi.ToStringArray([]string{"sts:AssumeRole"}),
					Principals: iam.GetPolicyDocumentStatementPrincipalArray{
						iam.GetPolicyDocumentStatementPrincipalArgs{
							Type:        pulumi.String("AWS"),
							Identifiers: pulumi.StringArray([]pulumi.StringInput{notificationIntegration.AwsSnsIamUserArn}),
						},
					},
					Conditions: iam.GetPolicyDocumentStatementConditionArray{
						iam.GetPolicyDocumentStatementConditionArgs{
							Test:     pulumi.String("StringEquals"),
							Variable: pulumi.String("sts:ExternalId"),
							Values:   pulumi.StringArray([]pulumi.StringInput{notificationIntegration.AwsSnsExternalId}),
						},
					},
				},
			},
		})

		notificationIAMRole, err := iam.NewRole(ctx, "antrea-sf-notification-iam-policy", &iam.RoleArgs{
			Name:             pulumi.String(notificationIAMRoleName),
			AssumeRolePolicy: notificationAssumeRolePolicyDocument.Json(),
		})
		if err != nil {
			return nil, nil, err
		}

		_, err = iam.NewRolePolicyAttachment(ctx, "antrea-sf-notification-iam-role-policy-attachment", &iam.RolePolicyAttachmentArgs{
			Role:      notificationIAMRole.Name,
			PolicyArn: notificationIAMPolicy.Arn,
		})
		if err != nil {
			return nil, nil, err
		}

		return notificationIntegration, notificationIAMRole, nil
	}
	return declareFunc
}

func declareSnowflakeDatabase(
	bucketName string,
	storageIntegration *snowflake.StorageIntegration,
	storageIAMRole *iam.Role,
	notificationIntegration *snowflake.NotificationIntegration,
	notificationIAMRole *iam.Role,
) func(ctx *pulumi.Context) error {
	declareFunc := func(ctx *pulumi.Context) error {
		db, err := snowflake.NewDatabase(ctx, "antrea-sf-db", &snowflake.DatabaseArgs{
			Name: pulumi.String(databaseName),
		})
		if err != nil {
			return err
		}

		schema, err := snowflake.NewSchema(ctx, "antrea-sf-schema", &snowflake.SchemaArgs{
			Database: db.Name,
			Name:     pulumi.String(schemaName),
		})
		if err != nil {
			return err
		}

		_, err = snowflake.NewStage(ctx, "antrea-sf-udf-stage", &snowflake.StageArgs{
			Database: db.Name,
			Schema:   schema.Name,
			Name:     pulumi.String(udfStageName),
		})
		if err != nil {
			return err
		}

		// IAMRoles need to be added as dependencies, but integrations are probbaly redundant
		dependencies := []pulumi.Resource{storageIntegration, storageIAMRole}
		if notificationIntegration != nil {
			dependencies = append(dependencies, notificationIntegration, notificationIAMRole)
		}

		_, err = snowflake.NewStage(ctx, "antrea-sf-ingestion-stage", &snowflake.StageArgs{
			Database:           db.Name,
			Schema:             schema.Name,
			Name:               pulumi.String(ingestionStageName),
			Url:                pulumi.Sprintf("s3://%s/%s/", bucketName, s3BucketPrefix),
			StorageIntegration: storageIntegration.Name,
		}, pulumi.DependsOn([]pulumi.Resource{storageIntegration, storageIAMRole}))
		if err != nil {
			return err
		}

		if flowRetentionDays > 0 {
			_, err := snowflake.NewTask(ctx, "antrea-sf-flow-deletion-task", &snowflake.TaskArgs{
				Database:                            db.Name,
				Schema:                              schema.Name,
				Name:                                pulumi.String(flowDeletionTaskName),
				Schedule:                            pulumi.String("USING CRON 0 0 * * * UTC"),
				SqlStatement:                        pulumi.Sprintf("DELETE FROM flows WHERE DATEDIFF(day, timeInserted, CURRENT_TIMESTAMP) > %d", flowRetentionDays),
				UserTaskManagedInitialWarehouseSize: pulumi.String("XSMALL"),
				Enabled:                             pulumi.Bool(true),
			})
			if err != nil {
				return err
			}
		}

		return nil
	}
	return declareFunc
}

func declare(bucketName string, snsTopicARN string) func(ctx *pulumi.Context) error {
	declareFunc := func(ctx *pulumi.Context) error {
		bucket, err := s3.NewBucket(
			ctx,
			"antrea-bucket",
			&s3.BucketArgs{
				Acl:    pulumi.String("private"),
				Bucket: pulumi.String(bucketName),
			},
			pulumi.RetainOnDelete(true),
			pulumi.Import(pulumi.ID(bucketName)),
			// this is necessary, or the previously configured lifecyle configuration
			// will be discared when updating the stack
			pulumi.IgnoreChanges([]string{"lifecycleRules"}),
		)
		if err != nil {
			return err
		}

		_, err = s3.NewBucketLifecycleConfigurationV2(ctx, "antrea-bucket-lifecycle-configuration", &s3.BucketLifecycleConfigurationV2Args{
			Bucket: bucket.ID(),
			Rules: s3.BucketLifecycleConfigurationV2RuleArray{
				&s3.BucketLifecycleConfigurationV2RuleArgs{
					Expiration: &s3.BucketLifecycleConfigurationV2RuleExpirationArgs{
						Days: pulumi.Int(flowRecordsRetentionDays),
					},
					Filter: &s3.BucketLifecycleConfigurationV2RuleFilterArgs{
						Prefix: pulumi.Sprintf("%s/", pulumi.String(s3BucketPrefix)),
					},
					Id:     pulumi.String(s3BucketPrefix),
					Status: pulumi.String("Enabled"),
				},
			},
		})
		if err != nil {
			return err
		}

		current, err := aws.GetCallerIdentity(ctx, nil, nil)
		if err != nil {
			return err
		}

		storageIntegration, storageIAMRole, err := declareSnowflakeIngestion(bucketName, current.AccountId)(ctx)
		if err != nil {
			return err
		}

		notificationIntegration, notificationIAMRole, err := declareSnowflakeErrorNotifications(snsTopicARN, current.AccountId)(ctx)
		if err != nil {
			return err
		}

		if err := declareSnowflakeDatabase(bucketName, storageIntegration, storageIAMRole, notificationIntegration, notificationIAMRole)(ctx); err != nil {
			return err
		}

		return nil
	}
	return declareFunc
}

func createTemporaryWorkdir() (string, error) {
	return os.MkdirTemp("", "antrea-pulumi")
}

func deleteTemporaryWorkdir(d string) {
	os.RemoveAll(d)
}

func installPulumiCLI(ctx context.Context, logger logr.Logger, dir string) error {
	cachedVersion, err := os.ReadFile(filepath.Join(dir, ".pulumi-version"))
	if err == nil && string(cachedVersion) == pulumiVersion {
		logger.Info("Pulumi CLI is already up-to-date")
		return nil
	}
	operatingSystem := runtime.GOOS
	arch := runtime.GOARCH
	target := operatingSystem
	if arch == "amd64" {
		target += "-x64"
	} else if arch == "arm64" {
		target += "-arm64"
	} else {
		return fmt.Errorf("arch not supported: %s", arch)
	}
	supportedTargets := map[string]bool{
		"darwin-arm64": true,
		"darwin-x64":   true,
		"linux-arm64":  true,
		"linux-x64":    true,
		"windows-x64":  true,
	}
	if _, ok := supportedTargets[target]; !ok {
		return fmt.Errorf("OS / arch combination is not supported: %s / %s", operatingSystem, arch)
	}
	url := fmt.Sprintf("https://github.com/pulumi/pulumi/releases/download/%s/pulumi-%s-%s.tar.gz", pulumiVersion, pulumiVersion, target)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	if err := os.MkdirAll(filepath.Join(dir, "pulumi"), 0755); err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, hdr.Name)
		logger.V(4).Info("Untarring", "path", hdr.Name)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := func() error {
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_RDWR, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			defer f.Close()

			// copy over contents
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".pulumi-version"), []byte(pulumiVersion), 0660); err != nil {
		logger.Error(err, "Error when writing pulumi version to cache file")
	}
	return nil
}

type Manager struct {
	logger      logr.Logger
	bucketName  string
	snsTopicARN string
	region      string
	workdir     string
}

func NewManager(logger logr.Logger, bucketName string, snsTopicARN string, region string, workdir string) *Manager {
	return &Manager{
		logger:      logger,
		bucketName:  bucketName,
		snsTopicARN: snsTopicARN,
		region:      region,
		workdir:     workdir,
	}
}

func (m *Manager) setup(ctx context.Context, workdir string) (auto.Stack, error) {
	logger := m.logger
	declareFunc := declare(m.bucketName, m.snsTopicARN)
	logger.Info("Creating stack")
	s, err := auto.UpsertStackInlineSource(
		ctx,
		stackName,
		projectName,
		declareFunc,
		auto.WorkDir(workdir),
		auto.Project(workspace.Project{
			Name:    tokens.PackageName(projectName),
			Runtime: workspace.NewProjectRuntimeInfo("go", nil),
			Main:    workdir,
			Backend: &workspace.ProjectBackend{
				URL: fmt.Sprintf("s3://%s/%s?region=%s", m.bucketName, "infra", m.region),
			},
		}),
		auto.EnvVars(map[string]string{
			// we do not store any secrets
			// additionally, any state file is only stored on disk temporarily and the
			// only persistent storage is in the S3 bucket
			// note that Pulumi does not not store credentials (e.g., AWScredentials)
			"PULUMI_CONFIG_PASSPHRASE": "",
		}),
	)
	if err != nil {
		return s, err
	}
	logger.Info("Created stack")
	w := s.Workspace()
	logger.Info("Installing AWS plugin")
	if err := w.InstallPlugin(ctx, "aws", pulumiAWSPluginVersion); err != nil {
		return s, fmt.Errorf("failed to install AWS Pulumi plugin: %w", err)
	}
	logger.Info("Installed AWS plugin")
	logger.Info("Installing Snowflake plugin")
	if err := w.InstallPlugin(ctx, "snowflake", pulumiSnowflakePluginVersion); err != nil {
		return s, fmt.Errorf("failed to install Snowflake Pulumi plugin: %w", err)
	}
	logger.Info("Installed Snowflake plugin")
	// set stack configuration specifying the AWS region to deploy
	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: m.region})
	return s, nil
}

func (m *Manager) run(ctx context.Context, destroy bool) error {
	logger := m.logger
	workdir := m.workdir
	if workdir == "" {
		var err error
		workdir, err = createTemporaryWorkdir()
		if err != nil {
			return err
		}
		logger.Info("Created temporary workdir", "path", workdir)
		defer deleteTemporaryWorkdir(workdir)
	} else {
		var err error
		workdir, err = filepath.Abs(workdir)
		if err != nil {
			return err
		}
	}
	logger.Info("Downloading and installing Pulumi")
	if err := installPulumiCLI(ctx, logger, workdir); err != nil {
		return fmt.Errorf("error when installing Pulumi: %w", err)
	}
	os.Setenv("PATH", filepath.Join(workdir, "pulumi"))
	logger.Info("Installed Pulumi")
	s, err := m.setup(ctx, workdir)
	if err != nil {
		return err
	}
	logger.Info("Refreshing stack")
	_, err = s.Refresh(ctx)
	if err != nil {
		return err
	}
	logger.Info("Refreshed stack")
	logger.Info("Updating stack")
	// wire up our update to stream progress to stdout
	stdoutStreamer := optup.ProgressStreams(os.Stdout)
	// res, err := s.Up(ctx, stdoutStreamer)
	_, err = s.Up(ctx, stdoutStreamer)
	if err != nil {
		return err
	}
	logger.Info("Updated stack")
	return nil
}

func (m *Manager) Onboard(ctx context.Context) error {
	return m.run(ctx, false)
}

func (m *Manager) Offboard(ctx context.Context) error {
	return m.run(ctx, true)
}
