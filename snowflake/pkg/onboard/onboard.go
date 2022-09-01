package onboard

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"

	"antrea.io/theia/snowflake/database"
	iamclient "antrea.io/theia/snowflake/pkg/aws/client/iam"
	s3client "antrea.io/theia/snowflake/pkg/aws/client/s3"
	snsclient "antrea.io/theia/snowflake/pkg/aws/client/sns"
	stsclient "antrea.io/theia/snowflake/pkg/aws/client/sts"
	"antrea.io/theia/snowflake/pkg/config"
	sf "antrea.io/theia/snowflake/pkg/snowflake"
)

const (
	schemaName           = "theia"
	ingestionStageName   = "flowstage"
	ingestionPipeName    = "flowpipe"
	flowsTableName       = "flows"
	udfStageName         = "udfstage"
	flowDeletionTaskName = "delete_stale_flows"
)

var (
	getIAMClient func(cfg aws.Config) iamclient.Interface = iamclient.GetClient
	getS3Client  func(cfg aws.Config) s3client.Interface  = s3client.GetClient
	getSNSClient func(cfg aws.Config) snsclient.Interface = snsclient.GetClient
	getSTSClient func(cfg aws.Config) stsclient.Interface = stsclient.GetClient
)

type Onboarder struct {
	logger    logr.Logger
	config    config.Config
	awsConfig aws.Config
	sfClient  sf.Client
}

func NewOnboarder(logger logr.Logger, config config.Config, awsConfig aws.Config, sfClient sf.Client) *Onboarder {
	return &Onboarder{
		logger:    logger,
		config:    config,
		awsConfig: awsConfig,
		sfClient:  sfClient,
	}
}

func (o *Onboarder) createIAMPolicyIfNotPresent(
	ctx context.Context,
	iamClient iamclient.Interface,
	accountID string,
	policyName string,
	policyDocument string,
) (string, error) {
	logger := o.logger
	policyARN := arn.ARN{
		Partition: "aws",
		Service:   "iam",
		AccountID: accountID,
		Resource:  fmt.Sprintf("policy/%s", policyName),
	}
	policyARNStr := policyARN.String()

	_, err := iamClient.GetPolicy(ctx, &iam.GetPolicyInput{
		PolicyArn: &policyARNStr,
	})
	var notFoundError *iamtypes.NoSuchEntityException
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("error when looking for IAM policy '%s': %w", policyARNStr, err)
	}
	if notFoundError == nil {
		logger.Info("IAM policy already exists", "name", policyName, "ARN", policyARNStr)
		return policyARNStr, nil
	}
	logger.Info("Need to create IAM policy", "name", policyName)

	// IAM policy for Snowflake to access the bucket
	createPolicyOut, err := iamClient.CreatePolicy(ctx, &iam.CreatePolicyInput{
		PolicyDocument: &policyDocument,
		PolicyName:     &policyName,
	})
	if err != nil {
		return "", fmt.Errorf("error when creating policy '%s': %w", policyName, err)
	}
	if *createPolicyOut.Policy.Arn != policyARNStr {
		return "", fmt.Errorf("unexpected policy ARN")
	}

	return policyARNStr, nil
}

func (o *Onboarder) createIAMRoleIfNotPresent(
	ctx context.Context,
	iamClient iamclient.Interface,
	roleName string,
	assumeRolePolicyDocument string,
) (string, error) {
	logger := o.logger
	getRoleOut, err := iamClient.GetRole(ctx, &iam.GetRoleInput{
		RoleName: &roleName,
	})
	var notFoundError *iamtypes.NoSuchEntityException
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("error when looking for IAM role '%s': %w", roleName, err)
	}
	if notFoundError == nil {
		logger.Info("IAM role already exists", "name", roleName)
		return *getRoleOut.Role.Arn, nil
	}
	logger.Info("Need to create IAM role", "name", roleName)

	// IAM role for Snowflake to access the bucket
	createRoleOut, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 &roleName,
		AssumeRolePolicyDocument: &assumeRolePolicyDocument,
	})
	if err != nil {
		return "", fmt.Errorf("error when creating role '%s': %w", roleName, err)
	}

	return *createRoleOut.Role.Arn, nil
}

func (o *Onboarder) updateIAMRolePolicy(
	ctx context.Context,
	iamClient iamclient.Interface,
	roleName string,
	policyDocument string,
) error {
	if _, err := iamClient.UpdateAssumeRolePolicy(ctx, &iam.UpdateAssumeRolePolicyInput{
		RoleName:       &roleName,
		PolicyDocument: &policyDocument,
	}); err != nil {
		return fmt.Errorf("error when updating trust policy for role '%s': %w", roleName, err)
	}
	return nil
}

func (o *Onboarder) attachIAMRolePolicy(
	ctx context.Context,
	iamClient iamclient.Interface,
	policyARN string,
	roleName string,
) error {
	o.logger.Info("Attaching policy to role", "policyARN", policyARN, "roleName", roleName)
	// it's ok to attach the same policy to the same role more than once
	if _, err := iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		PolicyArn: &policyARN,
		RoleName:  &roleName,
	}); err != nil {
		return fmt.Errorf("error when attaching policy '%s' to role '%s': %w", policyARN, roleName, err)
	}
	return nil
}

func (o *Onboarder) createSNSTopicIfNotPresent(
	ctx context.Context,
	snsClient snsclient.Interface,
	topicName string,
) (string, error) {
	o.logger.Info("Creating SNS topic", "name", topicName)
	// CreateTopic is idempotent
	output, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: &topicName,
	})
	if err != nil {
		return "", fmt.Errorf("error when creating SNS topic '%s': %w", topicName, err)
	}
	return *output.TopicArn, nil
}

func (o *Onboarder) configureBucketNotifications(
	ctx context.Context,
	s3Client s3client.Interface,
	bucket, region, sqsQueueARN string,
) error {
	logger := o.logger.WithValues("bucket", bucket)
	logger.Info("Configuring bucket notifications")
	output, err := s3Client.GetBucketNotificationConfiguration(ctx, &s3.GetBucketNotificationConfigurationInput{
		Bucket: &bucket,
	})
	if err != nil {
		return fmt.Errorf("error when getting bucket notification configuration for '%s': %w", bucket, err)
	}

	// From the Snowflake documentation:
	// Following AWS guidelines, Snowflake designates no more than one SQS
	// queue per S3 bucket. This SQS queue may be shared among multiple
	// buckets in the same AWS account. The SQS queue coordinates
	// notifications for all pipes connecting the external stages for the S3
	// bucket to the target tables. When a data file is uploaded into the
	// bucket, all pipes that match the stage directory path perform a
	// one-time load of the file into their corresponding target tables.
	//
	// So for our given bucket, we should always observe the same SQS queue ARN.

	if output != nil && len(output.QueueConfigurations) > 0 {
		logger.Info("Existing bucket notification configuration found")
		if len(output.QueueConfigurations) > 1 {
			return fmt.Errorf("too many queue configurations")
		}
		queueConfiguration := &output.QueueConfigurations[0]
		if *queueConfiguration.QueueArn != sqsQueueARN {
			return fmt.Errorf("unexpected SQS queue ARN")
		}
	}

	name := "Auto-ingest Snowflake"
	if _, err := s3Client.PutBucketNotificationConfiguration(ctx, &s3.PutBucketNotificationConfigurationInput{
		Bucket: &bucket,
		NotificationConfiguration: &s3types.NotificationConfiguration{
			QueueConfigurations: []s3types.QueueConfiguration{
				{
					QueueArn: &sqsQueueARN,
					Events: []s3types.Event{
						"s3:ObjectCreated:*",
					},
					Id: &name,
				},
			},
		},
	}); err != nil {
		return err
	}
	return nil
}

func (o *Onboarder) configureBucket(
	ctx context.Context,
	s3Client s3client.Interface,
) error {
	region := o.config.AWS.Region
	bucket := o.config.AWS.S3BucketName
	createBucket := o.config.AWS.CreateS3Bucket
	prefix := o.config.AWS.S3BucketPrefix
	flowRecordsRetentionDays := int(o.config.AWS.FlowRecordsRetentionDays)
	logger := o.logger.WithValues("bucket", bucket)
	logger.Info("Configuring S3 bucket")
	// Check if bucket exists
	_, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &bucket,
	})
	var notFoundError *s3types.NotFound
	if err != nil && !errors.As(err, &notFoundError) {
		return fmt.Errorf("error when looking for bucket '%s': %w", bucket, err)
	}
	if notFoundError != nil {
		logger.Info("S3 bucket does not exist")
		if !createBucket {
			return fmt.Errorf("bucket '%s' does not exist and I was not instructed to create it", bucket)
		}
		_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
			Bucket: &bucket,
			ACL:    s3types.BucketCannedACLPrivate,
			CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
				LocationConstraint: s3types.BucketLocationConstraint(region),
			},
		})
		if err != nil {
			return fmt.Errorf("error when creating bucket '%s': %w", bucket, err)
		}
		logger.Info("S3 bucket was created successfully")
	} else {
		logger.Info("S3 bucket already exists")
	}

	logger.Info("Configuring lifecycle for S3 bucket", "retentionDays", flowRecordsRetentionDays)
	if _, err := s3Client.PutBucketLifecycleConfiguration(ctx, &s3.PutBucketLifecycleConfigurationInput{
		Bucket: &bucket,
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
			Rules: []s3types.LifecycleRule{
				{
					Status: s3types.ExpirationStatusEnabled,
					Expiration: &s3types.LifecycleExpiration{
						Days: int32(flowRecordsRetentionDays),
					},
					Filter: &s3types.LifecycleRuleFilterMemberPrefix{
						Value: prefix,
					},
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("error when configuring lifecycle policy for bucket '%s': %w", bucket, err)
	}
	logger.Info("Configured S3 bucket")
	return nil
}

// we follow the steps in https://docs.snowflake.com/en/user-guide/data-load-snowpipe-auto-s3.html
func (o *Onboarder) configureSnowflakeIngestion(
	ctx context.Context,
	iamClient iamclient.Interface,
	accountID string,
) error {
	bucket := o.config.AWS.S3BucketName
	prefix := o.config.AWS.S3BucketPrefix
	policyName := o.config.AWS.SnowflakeAccess.StorageIntegration.IAMPolicyName
	roleName := o.config.AWS.SnowflakeAccess.StorageIntegration.IAMRoleName
	storageIntegrationName := o.config.AWS.SnowflakeAccess.StorageIntegration.StorageIntegrationName
	logger := o.logger
	logger.Info("Configuring Snowflake Ingestion")
	policyDocumentStr, err := getSnowflakeStoragePolicyDocument(bucket, prefix)
	if err != nil {
		return err
	}
	policyARN, err := o.createIAMPolicyIfNotPresent(ctx, iamClient, accountID, policyName, policyDocumentStr)
	if err != nil {
		return err
	}
	assumeRolePolicyDocumentStr, err := getSnowflakeAssumeRolePolicyDocument(accountID, "0000")
	if err != nil {
		return err
	}
	roleARN, err := o.createIAMRoleIfNotPresent(ctx, iamClient, roleName, assumeRolePolicyDocumentStr)
	if err != nil {
		return err
	}
	if err := o.attachIAMRolePolicy(ctx, iamClient, policyARN, roleName); err != nil {
		return err
	}
	logger.Info("Snowflake AWS access for Ingestion", "policyARN", policyARN, "roleARN", roleARN)
	s3BucketPath := fmt.Sprintf("s3://%s/%s/", bucket, prefix)
	userARN, externalID, err := o.sfClient.CreateStorageIntegration(ctx, storageIntegrationName, roleARN, s3BucketPath)
	if err != nil {
		return err
	}
	logger.Info("Snowflake AWS access for Ingestion", "userARN", userARN, "externalID", externalID)
	// we can now update the AssumeRolePolicy document with the correct user ARN and externalID
	assumeRolePolicyDocumentStr, err = getSnowflakeAssumeRolePolicyDocument(userARN, externalID)
	if err != nil {
		return err
	}

	if err := o.updateIAMRolePolicy(ctx, iamClient, roleName, assumeRolePolicyDocumentStr); err != nil {
		return err
	}
	logger.Info("Configured Snowflake Ingestion")
	return nil
}

// we follow the steps in https://docs.snowflake.com/en/user-guide/data-load-snowpipe-errors-sns.html
func (o *Onboarder) configureSnowflakeErrorNotifications(
	ctx context.Context,
	accountID string,
	iamClient iamclient.Interface,
	snsClient snsclient.Interface,
) error {
	snowflakeNotificationIntegration := o.config.AWS.SnowflakeAccess.NotificationIntegration
	snsTopicName := snowflakeNotificationIntegration.SNSTopicName
	policyName := snowflakeNotificationIntegration.IAMPolicyName
	roleName := snowflakeNotificationIntegration.IAMRoleName
	snowflakeNotificationIntegrationName := snowflakeNotificationIntegration.NotificationIntegrationName
	logger := o.logger
	logger.Info("Configuring Snowflake Error Notifications")
	snsTopicARN, err := o.createSNSTopicIfNotPresent(ctx, snsClient, snsTopicName)
	if err != nil {
		return err
	}
	policyDocumentStr, err := getSnowflakeNotificationPolicyDocument(snsTopicARN)
	if err != nil {
		return err
	}
	policyARN, err := o.createIAMPolicyIfNotPresent(ctx, iamClient, accountID, policyName, policyDocumentStr)
	if err != nil {
		return err
	}
	assumeRolePolicyDocumentStr, err := getSnowflakeAssumeRolePolicyDocument(accountID, "0000")
	if err != nil {
		return err
	}
	roleARN, err := o.createIAMRoleIfNotPresent(ctx, iamClient, roleName, assumeRolePolicyDocumentStr)
	if err != nil {
		return err
	}

	if err := o.attachIAMRolePolicy(ctx, iamClient, policyARN, roleName); err != nil {
		return err
	}
	logger.Info("Snowflake AWS access for Error Notifications", "policyARN", policyARN, "roleARN", roleARN)
	userARN, externalID, err := o.sfClient.CreateNotificationIntegration(
		ctx,
		snowflakeNotificationIntegrationName,
		snsTopicARN,
		roleARN,
	)
	if err != nil {
		return err
	}
	logger.Info("Snowflake AWS access for Error Notifications", "userARN", userARN, "externalID", externalID)
	assumeRolePolicyDocumentStr, err = getSnowflakeAssumeRolePolicyDocument(userARN, externalID)
	if err != nil {
		return err
	}
	if err := o.updateIAMRolePolicy(ctx, iamClient, roleName, assumeRolePolicyDocumentStr); err != nil {
		return err
	}
	logger.Info("Configured Snowflake Error Notifications")
	return nil
}

func (o *Onboarder) configureSnowflakeDatabase(
	ctx context.Context,
	s3Client s3client.Interface,
) error {
	dbName := o.config.DatabaseName
	flowRetentionDays := int(o.config.FlowRetentionDays)
	region := o.config.AWS.Region
	bucket := o.config.AWS.S3BucketName
	prefix := o.config.AWS.S3BucketPrefix
	storageIntegrationName := o.config.AWS.SnowflakeAccess.StorageIntegration.StorageIntegrationName
	notificationIntegrationName := o.config.AWS.SnowflakeAccess.NotificationIntegration.NotificationIntegrationName
	logger := o.logger
	logger.Info("Configuring Snowflake Database", "name", dbName)
	if err := o.sfClient.CreateDatabase(ctx, dbName); err != nil {
		return err
	}
	if err := o.sfClient.CreateSchema(ctx, schemaName); err != nil {
		return err
	}
	if err := o.sfClient.RunMigrations(ctx, database.Migrations, database.MigrationsPath); err != nil {
		return err
	}
	s3BucketPath := fmt.Sprintf("s3://%s/%s", bucket, prefix)
	if err := o.sfClient.CreateIngestionStage(ctx, ingestionStageName, s3BucketPath, storageIntegrationName); err != nil {
		return err
	}
	sqsQueueARN, err := o.sfClient.CreateAutoIngestPipe(ctx, ingestionPipeName, flowsTableName, ingestionStageName, notificationIntegrationName)
	if err != nil {
		return err
	}
	if err := o.sfClient.CreateUDFStage(ctx, udfStageName); err != nil {
		return err
	}
	if flowRetentionDays > 0 {
		if err := o.sfClient.CreateFlowDeletionTask(ctx, flowDeletionTaskName, "XSMALL", flowRetentionDays); err != nil {
			return err
		}
	} else {
		if err := o.sfClient.DeleteFlowDeletionTask(ctx, flowDeletionTaskName); err != nil {
			return err
		}
	}
	if err := o.configureBucketNotifications(ctx, s3Client, bucket, region, sqsQueueARN); err != nil {
		return err
	}
	logger.Info("Configured Snowflake Database", "name", dbName)
	return nil
}

func (o *Onboarder) Onboard(ctx context.Context) error {
	logger := o.logger
	iamClient := getIAMClient(o.awsConfig)
	s3Client := getS3Client(o.awsConfig)
	stsClient := getSTSClient(o.awsConfig)

	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("cannot retrieve identity: %w", err)
	}
	account := aws.ToString(identity.Account)
	logger.Info("AWS identity", "account", account)

	if err := o.configureBucket(ctx, s3Client); err != nil {
		return err
	}

	if err := o.configureSnowflakeIngestion(ctx, iamClient, account); err != nil {
		return err
	}

	snowflakeNotificationIntegration := o.config.AWS.SnowflakeAccess.NotificationIntegration
	if snowflakeNotificationIntegration != nil {
		snsClient := getSNSClient(o.awsConfig)
		if err := o.configureSnowflakeErrorNotifications(ctx, account, iamClient, snsClient); err != nil {
			return err
		}
	}

	if err := o.configureSnowflakeDatabase(ctx, s3Client); err != nil {
		return err
	}

	return nil
}
