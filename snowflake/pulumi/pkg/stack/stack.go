package stack

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/dustinkirkland/golang-petname"
	"github.com/go-logr/logr"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/s3"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/sns"
	"github.com/pulumi/pulumi-aws/sdk/v5/go/aws/sqs"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi-snowflake/sdk/go/snowflake"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optdestroy"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optup"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"github.com/pulumi/pulumi/sdk/v3/go/common/workspace"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"antrea.io/theia/snowflake/pulumi/database"
	sf "antrea.io/theia/snowflake/pulumi/pkg/snowflake"
)

const (
	dbProjectName        = "theia-infra-db"
	dbObjectsProjectName = "theia-infra-db-objects"

	pulumiVersion                = "v3.39.3"
	pulumiAWSPluginVersion       = "v5.13.0"
	pulumiSnowflakePluginVersion = "v0.13.0"
	pulumiRandomPluginVersion    = "v4.8.2"

	flowRecordsRetentionDays = 7
	s3BucketNamePrefix       = "antrea-flows-"
	s3BucketFlowsFolder      = "flows"
	snsTopicNamePrefix       = "antrea-flows-"
	sqsQueueNamePrefix       = "antrea-flows-"
	// how long will Snowpipe error notifications be saved in SQS queue
	sqsMessageRetentionSeconds = 7 * 24 * 3600

	storageIAMRoleNamePrefix     = "antrea-sf-storage-iam-role-"
	storageIAMPolicyNamePrefix   = "antrea-sf-storage-iam-policy-"
	storageIntegrationNamePrefix = "ANTREA_FLOWS_STORAGE_INTEGRATION_"

	notificationIAMRoleNamePrefix     = "antrea-sf-notification-iam-role-"
	notificationIAMPolicyNamePrefix   = "antrea-sf-notification-iam-policy"
	notificationIntegrationNamePrefix = "ANTREA_FLOWS_NOTIFICATION_INTEGRATION_"

	databaseNamePrefix = "ANTREA_"

	schemaName           = "THEIA"
	flowRetentionDays    = 30
	flowDeletionTaskName = "DELETE_STALE_FLOWS"
	udfStageName         = "UDFS"
	ingestionStageName   = "FLOWSTAGE"
	autoIngestPipeName   = "FLOWPIPE"

	// do not change!!!
	flowsTableName = "FLOWS"
)

type pulumiPlugin struct {
	name    string
	version string
}

func declareSnowflakeIngestion(randomString *random.RandomString, bucket *s3.BucketV2, accountID string) func(ctx *pulumi.Context) (*snowflake.StorageIntegration, *iam.Role, error) {
	declareFunc := func(ctx *pulumi.Context) (*snowflake.StorageIntegration, *iam.Role, error) {
		storageIntegration, err := snowflake.NewStorageIntegration(ctx, "antrea-sf-storage-integration", &snowflake.StorageIntegrationArgs{
			Name:                    pulumi.Sprintf("%s%s", storageIntegrationNamePrefix, randomString.ID()),
			Type:                    pulumi.String("EXTERNAL_STAGE"),
			Enabled:                 pulumi.Bool(true),
			StorageAllowedLocations: pulumi.StringArray([]pulumi.StringInput{pulumi.Sprintf("s3://%s/%s/", bucket.ID(), s3BucketFlowsFolder)}),
			StorageProvider:         pulumi.String("S3"),
			StorageAwsRoleArn:       pulumi.Sprintf("arn:aws:iam::%s:role/%s%s", accountID, storageIAMRoleNamePrefix, randomString.ID()),
		})
		if err != nil {
			return nil, nil, err
		}

		storagePolicyDocument := iam.GetPolicyDocumentOutput(ctx, iam.GetPolicyDocumentOutputArgs{
			Statements: iam.GetPolicyDocumentStatementArray{
				iam.GetPolicyDocumentStatementArgs{
					Sid:       pulumi.String("1"),
					Effect:    pulumi.String("Allow"),
					Actions:   pulumi.ToStringArray([]string{"s3:GetObject", "s3:GetObjectVersion"}),
					Resources: pulumi.StringArray([]pulumi.StringInput{pulumi.Sprintf("arn:aws:s3:::%s/%s/*", bucket.ID(), s3BucketFlowsFolder)}),
				},
				iam.GetPolicyDocumentStatementArgs{
					Sid:       pulumi.String("2"),
					Effect:    pulumi.String("Allow"),
					Actions:   pulumi.ToStringArray([]string{"s3:ListBucket"}),
					Resources: pulumi.StringArray([]pulumi.StringInput{pulumi.Sprintf("arn:aws:s3:::%s", bucket.ID())}),
					Conditions: iam.GetPolicyDocumentStatementConditionArray{
						iam.GetPolicyDocumentStatementConditionArgs{
							Test:     pulumi.String("StringLike"),
							Variable: pulumi.String("s3:prefix"),
							Values:   pulumi.StringArray([]pulumi.StringInput{pulumi.Sprintf("%s/*", s3BucketFlowsFolder)}),
						},
					},
				},
				iam.GetPolicyDocumentStatementArgs{
					Sid:       pulumi.String("3"),
					Effect:    pulumi.String("Allow"),
					Actions:   pulumi.ToStringArray([]string{"s3:GetBucketLocation"}),
					Resources: pulumi.StringArray([]pulumi.StringInput{pulumi.Sprintf("arn:aws:s3:::%s", bucket.ID())}),
				},
			},
		})

		// For some reason pulumi reports a diff on every refresh, when in fact the resource
		// does not need to be updated and is not actually updated during the "up" stage.
		// See https://github.com/pulumi/pulumi-aws/issues/2024
		storageIAMPolicy, err := iam.NewPolicy(ctx, "antrea-sf-storage-iam-policy", &iam.PolicyArgs{
			Name:   pulumi.Sprintf("%s%s", storageIAMPolicyNamePrefix, randomString.ID()),
			Policy: storagePolicyDocument.Json(),
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
			Name:             pulumi.Sprintf("%s%s", storageIAMRoleNamePrefix, randomString.ID()),
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

func declareSnowflakeErrorNotifications(randomString *random.RandomString, snsTopic *sns.Topic, accountID string) func(ctx *pulumi.Context) (*snowflake.NotificationIntegration, *iam.Role, error) {
	declareFunc := func(ctx *pulumi.Context) (*snowflake.NotificationIntegration, *iam.Role, error) {
		notificationIntegrationName := randomString.Result.ApplyT(func(suffix string) string {
			return fmt.Sprintf("%s%s", notificationIntegrationNamePrefix, strings.ToUpper(suffix))
		}).(pulumi.StringOutput)
		notificationIntegration, err := snowflake.NewNotificationIntegration(ctx, "antrea-sf-notification-integration", &snowflake.NotificationIntegrationArgs{
			Name:                 notificationIntegrationName,
			AwsSnsRoleArn:        pulumi.Sprintf("arn:aws:iam::%s:role/%s%s", accountID, notificationIAMRoleNamePrefix, randomString.ID()),
			AwsSnsTopicArn:       snsTopic.ID(),
			Direction:            pulumi.String("OUTBOUND"),
			Enabled:              pulumi.Bool(true),
			NotificationProvider: pulumi.String("AWS_SNS"),
			Type:                 pulumi.String("QUEUE"),
		})
		if err != nil {
			return nil, nil, err
		}
		ctx.Export("notificationIntegrationName", notificationIntegration.Name)

		notificationPolicyDocument := iam.GetPolicyDocumentOutput(ctx, iam.GetPolicyDocumentOutputArgs{
			Statements: iam.GetPolicyDocumentStatementArray{
				iam.GetPolicyDocumentStatementArgs{
					Sid:       pulumi.String("1"),
					Effect:    pulumi.String("Allow"),
					Actions:   pulumi.ToStringArray([]string{"sns:Publish"}),
					Resources: pulumi.StringArray([]pulumi.StringInput{snsTopic.ID()}),
				},
			},
		})

		notificationIAMPolicy, err := iam.NewPolicy(ctx, "antrea-sf-notification-iam-policy", &iam.PolicyArgs{
			Name:   pulumi.Sprintf("%s%s", notificationIAMPolicyNamePrefix, randomString.ID()),
			Policy: notificationPolicyDocument.Json(),
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
			Name:             pulumi.Sprintf("%s%s", notificationIAMRoleNamePrefix, randomString.ID()),
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
	randomString *random.RandomString,
	bucket *s3.BucketV2,
	storageIntegration *snowflake.StorageIntegration,
	storageIAMRole *iam.Role,
	notificationIntegration *snowflake.NotificationIntegration,
	notificationIAMRole *iam.Role,
) func(ctx *pulumi.Context) error {
	declareFunc := func(ctx *pulumi.Context) error {
		databaseName := randomString.Result.ApplyT(func(suffix string) string {
			return fmt.Sprintf("%s%s", databaseNamePrefix, strings.ToUpper(suffix))
		}).(pulumi.StringOutput)
		db, err := snowflake.NewDatabase(ctx, "antrea-sf-db", &snowflake.DatabaseArgs{
			Name: databaseName,
		})
		if err != nil {
			return err
		}
		ctx.Export("databaseName", db.Name)

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
			Url:                pulumi.Sprintf("s3://%s/%s/", bucket.ID(), s3BucketFlowsFolder),
			StorageIntegration: storageIntegration.Name,
		}, pulumi.DependsOn([]pulumi.Resource{storageIntegration, storageIAMRole}))
		if err != nil {
			return err
		}

		return nil
	}
	return declareFunc
}

func declareDBStack() func(ctx *pulumi.Context) error {
	declareFunc := func(ctx *pulumi.Context) error {
		randomString, err := random.NewRandomString(ctx, "antrea-flows-random-pet-suffix", &random.RandomStringArgs{
			Length:  pulumi.Int(16),
			Lower:   pulumi.Bool(true),
			Upper:   pulumi.Bool(false),
			Number:  pulumi.Bool(true),
			Special: pulumi.Bool(false),
		})
		bucket, err := s3.NewBucketV2(ctx, "antrea-flows-bucket", &s3.BucketV2Args{
			Bucket: pulumi.Sprintf("%s%s", s3BucketNamePrefix, randomString.ID()),
		})
		if err != nil {
			return err
		}
		ctx.Export("bucketID", bucket.ID())

		_, err = s3.NewBucketLifecycleConfigurationV2(ctx, "antrea-flows-bucket-lifecycle-configuration", &s3.BucketLifecycleConfigurationV2Args{
			Bucket: bucket.ID(),
			Rules: s3.BucketLifecycleConfigurationV2RuleArray{
				&s3.BucketLifecycleConfigurationV2RuleArgs{
					Expiration: &s3.BucketLifecycleConfigurationV2RuleExpirationArgs{
						Days: pulumi.Int(flowRecordsRetentionDays),
					},
					Filter: &s3.BucketLifecycleConfigurationV2RuleFilterArgs{
						Prefix: pulumi.Sprintf("%s/", pulumi.String(s3BucketFlowsFolder)),
					},
					Id:     pulumi.String(s3BucketFlowsFolder),
					Status: pulumi.String("Enabled"),
				},
			},
		})
		if err != nil {
			return err
		}

		sqsQueue, err := sqs.NewQueue(ctx, "antrea-flows-sqs-queue", &sqs.QueueArgs{
			Name:                    pulumi.Sprintf("%s%s", sqsQueueNamePrefix, randomString.ID()),
			MessageRetentionSeconds: pulumi.Int(sqsMessageRetentionSeconds),
		})
		if err != nil {
			return err
		}

		snsTopic, err := sns.NewTopic(ctx, "antrea-flows-sns-topic", &sns.TopicArgs{
			Name: pulumi.Sprintf("%s%s", snsTopicNamePrefix, randomString.ID()),
		})
		if err != nil {
			return err
		}

		sqsQueuePolicyDocument := iam.GetPolicyDocumentOutput(ctx, iam.GetPolicyDocumentOutputArgs{
			Statements: iam.GetPolicyDocumentStatementArray{
				iam.GetPolicyDocumentStatementArgs{
					Sid:    pulumi.String("1"),
					Effect: pulumi.String("Allow"),
					Principals: iam.GetPolicyDocumentStatementPrincipalArray{
						iam.GetPolicyDocumentStatementPrincipalArgs{
							Type:        pulumi.String("Service"),
							Identifiers: pulumi.ToStringArray([]string{"sns.amazonaws.com"}),
						},
					},
					Actions:   pulumi.ToStringArray([]string{"sqs:SendMessage"}),
					Resources: pulumi.StringArray([]pulumi.StringInput{sqsQueue.Arn}),
					Conditions: iam.GetPolicyDocumentStatementConditionArray{
						iam.GetPolicyDocumentStatementConditionArgs{
							Test:     pulumi.String("ArnEquals"),
							Variable: pulumi.String("aws:SourceArn"),
							Values:   pulumi.StringArray([]pulumi.StringInput{snsTopic.Arn}),
						},
					},
				},
			},
		})

		_, err = sqs.NewQueuePolicy(ctx, "antrea-flows-sqs-queue-policy", &sqs.QueuePolicyArgs{
			QueueUrl: sqsQueue.ID(),
			Policy:   sqsQueuePolicyDocument.Json(),
		})
		if err != nil {
			return err
		}

		_, err = sns.NewTopicSubscription(ctx, "antrea-flows-sns-subscription", &sns.TopicSubscriptionArgs{
			Endpoint: sqsQueue.Arn,
			Protocol: pulumi.String("sqs"),
			Topic:    snsTopic.Arn,
		})
		if err != nil {
			return err
		}

		current, err := aws.GetCallerIdentity(ctx, nil, nil)
		if err != nil {
			return err
		}

		storageIntegration, storageIAMRole, err := declareSnowflakeIngestion(randomString, bucket, current.AccountId)(ctx)
		if err != nil {
			return err
		}

		notificationIntegration, notificationIAMRole, err := declareSnowflakeErrorNotifications(randomString, snsTopic, current.AccountId)(ctx)
		if err != nil {
			return err
		}

		if err := declareSnowflakeDatabase(randomString, bucket, storageIntegration, storageIAMRole, notificationIntegration, notificationIAMRole)(ctx); err != nil {
			return err
		}

		return nil
	}
	return declareFunc
}

func declareDBObjectsStack(bucketID string, databaseName string, ingestionStageName string, notificationIntegrationName string) func(ctx *pulumi.Context) error {
	declareFunc := func(ctx *pulumi.Context) error {
		pipeArgs := &snowflake.PipeArgs{
			Database:         pulumi.String(databaseName),
			Schema:           pulumi.String(schemaName),
			Name:             pulumi.String(autoIngestPipeName),
			AutoIngest:       pulumi.Bool(true),
			ErrorIntegration: pulumi.String(notificationIntegrationName),
			// FQN required for table and stage, see https://github.com/pulumi/pulumi-snowflake/issues/129
			CopyStatement: pulumi.Sprintf("COPY INTO %s.%s.%s FROM @%s.%s.%s FILE_FORMAT = (TYPE = 'CSV')", databaseName, schemaName, flowsTableName, databaseName, schemaName, ingestionStageName),
		}

		// a bit of defensive programming: explicit dependency on ingestionStage may not be strictly required
		pipe, err := snowflake.NewPipe(ctx, "antrea-sf-auto-ingest-pipe", pipeArgs)
		if err != nil {
			return err
		}

		_, err = s3.NewBucketNotification(ctx, "antrea-flows-bucket-notification", &s3.BucketNotificationArgs{
			Bucket: pulumi.String(bucketID),
			Queues: s3.BucketNotificationQueueArray{
				&s3.BucketNotificationQueueArgs{
					QueueArn: pipe.NotificationChannel,
					Events: pulumi.StringArray{
						pulumi.String("s3:ObjectCreated:*"),
					},
					FilterPrefix: pulumi.Sprintf("%s/", s3BucketFlowsFolder),
					Id:           pulumi.String("Auto-ingest Snowflake"),
				},
			},
		})
		if err != nil {
			return err
		}

		if flowRetentionDays > 0 {
			_, err := snowflake.NewTask(ctx, "antrea-sf-flow-deletion-task", &snowflake.TaskArgs{
				Database:                            pulumi.String(databaseName),
				Schema:                              pulumi.String(schemaName),
				Name:                                pulumi.String(flowDeletionTaskName),
				Schedule:                            pulumi.String("USING CRON 0 0 * * * UTC"),
				SqlStatement:                        pulumi.Sprintf("DELETE FROM %s WHERE DATEDIFF(day, timeInserted, CURRENT_TIMESTAMP) > %d", flowsTableName, flowRetentionDays),
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
	logger            logr.Logger
	stackName         string
	infraBucketName   string
	infraBucketRegion string
	region            string
	warehouseName     string
	workdir           string
}

func NewManager(
	logger logr.Logger,
	stackName string,
	infraBucketName string,
	infraBucketRegion string,
	region string,
	warehouseName string,
	workdir string,
) *Manager {
	return &Manager{
		logger:            logger.WithValues("stack", stackName),
		stackName:         stackName,
		infraBucketName:   infraBucketName,
		infraBucketRegion: infraBucketRegion,
		region:            region,
		warehouseName:     warehouseName,
		workdir:           workdir,
	}
}

func (m *Manager) setup(
	ctx context.Context,
	projectName string,
	stackName string,
	workdir string,
	requiredPlugins []pulumiPlugin,
	declareFunc func(ctx *pulumi.Context) error,
) (auto.Stack, error) {
	logger := m.logger.WithValues("project", projectName)
	logger.Info("Creating stack")
	s, err := auto.UpsertStackInlineSource(
		ctx,
		fmt.Sprintf("%s.%s", projectName, stackName),
		projectName,
		declareFunc,
		auto.WorkDir(workdir),
		auto.Project(workspace.Project{
			Name:    tokens.PackageName(projectName),
			Runtime: workspace.NewProjectRuntimeInfo("go", nil),
			Main:    workdir,
			Backend: &workspace.ProjectBackend{
				URL: fmt.Sprintf("s3://%s/%s?region=%s", m.infraBucketName, "antrea-flows-infra", m.infraBucketRegion),
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
	for _, plugin := range requiredPlugins {
		logger.Info("Installing Pulumi plugin", "plugin", plugin.name)
		if err := w.InstallPlugin(ctx, plugin.name, plugin.version); err != nil {
			return s, fmt.Errorf("failed to install Pulumi plugin %s: %w", plugin.name, err)
		}
		logger.Info("Installed Pulumi plugin", "plugin", plugin.name)
	}
	// set stack configuration specifying the AWS region to deploy
	s.SetConfig(ctx, "aws:region", auto.ConfigValue{Value: m.region})
	logger.Info("Refreshing stack")
	_, err = s.Refresh(ctx)
	if err != nil {
		return s, err
	}
	logger.Info("Refreshed stack")
	return s, nil
}

func (m *Manager) runSnowflakeMigrations(ctx context.Context, databaseName string) error {
	logger := m.logger

	dsn, sfCfg, err := sf.GetDSN(sf.SetDatabase(databaseName), sf.SetSchema(schemaName))
	if err != nil {
		return fmt.Errorf("failed to create DSN from Config: %v, err: %w", sfCfg, err)
	}

	db, err := sql.Open("snowflake", dsn)
	if err != nil {
		return fmt.Errorf("failed to connect to %v: %w", dsn, err)
	}
	defer db.Close()

	sfClient := sf.NewClient(db, logger)

	warehouseName := m.warehouseName
	if warehouseName == "" {
		warehouseName = strings.ToUpper(petname.Generate(3, "_"))
		warehouseSize := sf.WarehouseSizeType("XSMALL")
		autoSuspend := int32(60) // minimum value
		logger.Info("Creating Snowflake warehouse", "name", warehouseName, "size", warehouseSize)
		if err := sfClient.CreateWarehouse(ctx, warehouseName, sf.WarehouseConfig{
			Size:        &warehouseSize,
			AutoSuspend: &autoSuspend,
		}); err != nil {
			return fmt.Errorf("error when creating Snowflake warehouse: %w", err)
		}
		defer func() {
			logger.Info("Deleting Snowflake warehouse", "name", warehouseName)
			if err := sfClient.DropWarehouse(ctx, warehouseName); err != nil {
				logger.Error(err, "Failed to delete temporary warehouse, please do it manually", "name", warehouseName)
			}
		}()
	}

	logger.Info("Using Snowflake warehouse", "name", warehouseName)
	if err := sfClient.UseWarehouse(ctx, warehouseName); err != nil {
		return err
	}

	logger.Info("Migrating Snowflake database", "name", databaseName)
	dbm := NewDBMigrator(db, logger)
	if err := dbm.DBMigrate(ctx, database.Migrations, database.MigrationsPath); err != nil {
		return err
	}
	logger.Info("Migrated Snowflake database", "name", databaseName)
	return nil
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

	dbStackPlugins := []pulumiPlugin{
		{name: "aws", version: pulumiAWSPluginVersion},
		{name: "snowflake", version: pulumiSnowflakePluginVersion},
		{name: "random", version: pulumiRandomPluginVersion},
	}
	dbObjectsStackPlugins := []pulumiPlugin{
		{name: "snowflake", version: pulumiSnowflakePluginVersion},
	}
	dbStack, err := m.setup(ctx, dbProjectName, m.stackName, workdir, dbStackPlugins, declareDBStack())
	if err != nil {
		return err
	}

	destroyFunc := func(projectName string, s *auto.Stack) error {
		logger := logger.WithValues("project", projectName)
		logger.Info("Destroying stack")
		// wire up our destroy to stream progress to stdout
		stdoutStreamer := optdestroy.ProgressStreams(os.Stdout)
		if _, err := s.Destroy(ctx, stdoutStreamer); err != nil {
			return err
		}
		logger.Info("Destroyed stack")
		logger.Info("Removing stack")
		if err := s.Workspace().RemoveStack(ctx, s.Name()); err != nil {
			return err
		}
		logger.Info("Removed stack")
		return nil
	}

	getFirstStackOutputs := func() (bucketID, databaseName, notificationIntegrationName string, err error) {
		outs, err := dbStack.Outputs(ctx)
		if err != nil {
			err = fmt.Errorf("failed to get outputs from first stack: %w", err)
			return
		}
		var ok bool
		bucketID, ok = outs["bucketID"].Value.(string)
		if !ok {
			err = fmt.Errorf("failed to get bucketID from first stack outputs")
			return
		}
		databaseName, ok = outs["databaseName"].Value.(string)
		if !ok {
			err = fmt.Errorf("failed to get databaseName from first stack outputs")
			return
		}
		notificationIntegrationName, ok = outs["notificationIntegrationName"].Value.(string)
		if !ok {
			err = fmt.Errorf("failed to get notificationIntegrationName from first stack outputs")
			return
		}
		return
	}

	if destroy {
		// destroy stacks in reverse order
		bucketID, databaseName, notificationIntegrationName, err := getFirstStackOutputs()
		if err != nil {
			return err
		}
		dbObjectsStack, err := m.setup(ctx, dbObjectsProjectName, m.stackName, workdir, dbObjectsStackPlugins, declareDBObjectsStack(bucketID, databaseName, ingestionStageName, notificationIntegrationName))
		if err != nil {
			return err
		}
		if err := destroyFunc(dbObjectsProjectName, &dbObjectsStack); err != nil {
			return err
		}
		if err := destroyFunc(dbProjectName, &dbStack); err != nil {
			return err
		}
		// return early
		return nil
	}

	updateFunc := func(projectName string, s *auto.Stack) error {
		logger := logger.WithValues("project", projectName)
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

	if err := updateFunc(dbProjectName, &dbStack); err != nil {
		return err
	}
	bucketID, databaseName, notificationIntegrationName, err := getFirstStackOutputs()
	if err != nil {
		return err
	}
	if err := m.runSnowflakeMigrations(ctx, databaseName); err != nil {
		return err
	}
	dbObjectsStack, err := m.setup(ctx, dbObjectsProjectName, m.stackName, workdir, dbObjectsStackPlugins, declareDBObjectsStack(bucketID, databaseName, ingestionStageName, notificationIntegrationName))
	if err := updateFunc(dbObjectsProjectName, &dbObjectsStack); err != nil {
		return err
	}

	return nil
}

func (m *Manager) Onboard(ctx context.Context) error {
	return m.run(ctx, false)
}

func (m *Manager) Offboard(ctx context.Context) error {
	return m.run(ctx, true)
}
