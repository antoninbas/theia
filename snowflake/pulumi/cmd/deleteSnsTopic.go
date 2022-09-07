/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"time"

	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/spf13/cobra"

	snsclient "antrea.io/theia/snowflake/pulumi/pkg/aws/client/sns"
)

// deleteSNSTopicCmd represents the delete-sns-topic command
var deleteSNSTopicCmd = &cobra.Command{
	Use:   "delete-sns-topic",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		region, _ := cmd.Flags().GetString("region")
		snsTopicARN, _ := cmd.Flags().GetString("arn")
		arn, err := awsarn.Parse(snsTopicARN)
		if err != nil {
			return fmt.Errorf("invalid ARN '%s': %w", snsTopicARN, err)
		}
		if region != "" && arn.Region != region {
			return fmt.Errorf("region conflict between --region flag and ARN region")
		}
		region = arn.Region
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return fmt.Errorf("unable to load AWS SDK config: %w", err)

		}
		snsClient := snsclient.GetClient(awsCfg)
		return deleteSNSTopic(ctx, snsClient, snsTopicARN)
	},
}

func deleteSNSTopic(ctx context.Context, snsClient snsclient.Interface, arn string) error {
	logger := logger.WithValues("ARN", arn)
	logger.Info("Deleting SNS topic")
	// DeleteTopic is idempotent
	if _, err := snsClient.DeleteTopic(ctx, &sns.DeleteTopicInput{
		TopicArn: &arn,
	}); err != nil {
		return fmt.Errorf("error when deleting SNS topic '%s': %w", arn, err)
	}
	logger.Info("Deleted SNS topic")
	return nil
}

func init() {
	rootCmd.AddCommand(deleteSNSTopicCmd)
	deleteSNSTopicCmd.Flags().String("region", "", "Region where SNS Topic should be deleted")
	deleteSNSTopicCmd.Flags().String("arn", "", "ARN of SNS Topic to delete")
	deleteSNSTopicCmd.MarkFlagRequired("arn")
}
