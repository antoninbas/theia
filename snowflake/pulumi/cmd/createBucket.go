/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/dustinkirkland/golang-petname"
	"github.com/spf13/cobra"

	s3client "antrea.io/theia/snowflake/pulumi/pkg/aws/client/s3"
)

// createBucketCmd represents the createBucket command
var createBucketCmd = &cobra.Command{
	Use:   "create-bucket",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		region, _ := cmd.Flags().GetString("region")
		bucketName, _ := cmd.Flags().GetString("name")
		bucketPrefix, _ := cmd.Flags().GetString("prefix")
		if bucketName == "" {
			suffix := petname.Generate(4, "-")
			bucketName = fmt.Sprintf("%s-%s", bucketPrefix, suffix)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return fmt.Errorf("unable to load AWS SDK config: %w", err)

		}
		s3Client := s3client.GetClient(awsCfg)
		return createBucket(ctx, s3Client, bucketName, region)
	},
}

func createBucket(ctx context.Context, s3Client s3client.Interface, name string, region string) error {
	logger := logger.WithValues("bucket", name, "region", region)
	logger.Info("Checking if S3 bucket exists")
	_, err := s3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: &name,
	})
	var notFoundError *s3types.NotFound
	if err != nil && !errors.As(err, &notFoundError) {
		return fmt.Errorf("error when looking for bucket '%s': %w", name, err)
	}
	if notFoundError == nil {
		logger.Info("S3 bucket already exists")
		return nil
	}
	logger.Info("Creating S3 bucket")
	if _, err := s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: &name,
		ACL:    s3types.BucketCannedACLPrivate,
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		},
	}); err != nil {
		return fmt.Errorf("error when creating bucket '%s': %w", name, err)
	}
	logger.Info("Created S3 bucket")
	return nil
}

func init() {
	rootCmd.AddCommand(createBucketCmd)

	createBucketCmd.Flags().String("region", defaultRegion, "Region where bucket should be created")
	createBucketCmd.Flags().String("name", "", "Name of bucket to create")
	createBucketCmd.Flags().String("prefix", "antrea", "Prefix to use for bucket name (with auto-generated suffix)")
}
