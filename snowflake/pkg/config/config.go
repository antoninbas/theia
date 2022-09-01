package config

import (
	"fmt"
)

type SnowflakeStorageIntegrationConfig struct {
	IAMPolicyName          string `yaml:"iamPolicyName"`
	IAMRoleName            string `yaml:"iamRoleName"`
	StorageIntegrationName string `yaml:"storageIntegrationName"`
}

type SnowflakeNotificationIntegrationConfig struct {
	IAMPolicyName               string `yaml:"iamPolicyName"`
	IAMRoleName                 string `yaml:"iamRoleName"`
	NotificationIntegrationName string `yaml:"notificationIntegrationName"`
	SNSTopicName                string `yaml:"snsTopicName"`
}

type SnowflakeAccessConfig struct {
	StorageIntegration      SnowflakeStorageIntegrationConfig       `yaml:"storageIntegration"`
	NotificationIntegration *SnowflakeNotificationIntegrationConfig `yaml:"notificationIntegration,omitempty"`
}

type AWSConfig struct {
	Region                   string                `yaml:"region"`
	S3BucketName             string                `yaml:"s3BucketName"`
	S3BucketPrefix           string                `yaml:"s3BucketPrefix"`
	CreateS3Bucket           bool                  `yaml:"createS3Bucket"`
	FlowRecordsRetentionDays int32                 `yaml:"flowRecordsRetentionDays"`
	SnowflakeAccess          SnowflakeAccessConfig `yaml:"snowflakeAccess"`
}

type Config struct {
	AWS               AWSConfig `yaml:"aws"`
	DatabaseName      string    `yaml:"databaseName"`
	FlowRetentionDays int32     `yaml:"flowRetentionDays"`
}

func (c *Config) ValidateAndSetDefaults() error {
	if c.DatabaseName == "" {
		c.DatabaseName = "antrea"
	}
	if c.AWS.S3BucketName == "" {
		return fmt.Errorf("s3BucketName is required")
	}
	if c.AWS.Region == "" {
		c.AWS.Region = "us-west-2"
	}
	if c.AWS.S3BucketPrefix == "" {
		c.AWS.S3BucketPrefix = "antrea-flows"
	}
	if c.AWS.FlowRecordsRetentionDays == 0 {
		c.AWS.FlowRecordsRetentionDays = 3
	}
	snowflake := &c.AWS.SnowflakeAccess
	if snowflake.StorageIntegration.IAMPolicyName == "" {
		snowflake.StorageIntegration.IAMPolicyName = "sf-access-iam-policy"
	}
	if snowflake.StorageIntegration.IAMRoleName == "" {
		snowflake.StorageIntegration.IAMRoleName = "sf-access-iam-role"
	}
	if snowflake.StorageIntegration.StorageIntegrationName == "" {
		snowflake.StorageIntegration.StorageIntegrationName = "antrea_flows"
	}
	if snowflake.NotificationIntegration != nil {
		if snowflake.NotificationIntegration.SNSTopicName == "" {
			return fmt.Errorf("snsTopicName is required")
		}
		if snowflake.NotificationIntegration.IAMPolicyName == "" {
			snowflake.NotificationIntegration.IAMPolicyName = "sf-notification-iam-policy"
		}
		if snowflake.NotificationIntegration.IAMRoleName == "" {
			snowflake.NotificationIntegration.IAMRoleName = "sf-notification-iam-role"
		}
		if snowflake.NotificationIntegration.NotificationIntegrationName == "" {
			snowflake.NotificationIntegration.NotificationIntegrationName = "antrea_flows_notifications"
		}
	}
	return nil
}
