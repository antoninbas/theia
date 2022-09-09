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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/spf13/cobra"

	sqsclient "antrea.io/theia/snowflake/pulumi/pkg/aws/client/sqs"
)

// receiveSqsMessageCmd represents the receiveSqsMessage command
var receiveSqsMessageCmd = &cobra.Command{
	Use:   "receive-sqs-message",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		region, _ := cmd.Flags().GetString("region")
		sqsQueueARN, _ := cmd.Flags().GetString("queue-arn")
		delete, _ := cmd.Flags().GetBool("delete")
		arn, err := awsarn.Parse(sqsQueueARN)
		if err != nil {
			return fmt.Errorf("invalid ARN '%s': %w", sqsQueueARN, err)
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
		sqsClient := sqsclient.GetClient(awsCfg)
		return receiveSQSMessage(ctx, sqsClient, arn.Resource, delete)
	},
}

func receiveSQSMessage(ctx context.Context, sqsClient sqsclient.Interface, queueName string, delete bool) error {
	queueURL, err := func() (string, error) {
		output, err := sqsClient.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
			QueueName: &queueName,
		})
		if err != nil {
			return "", err
		}
		return *output.QueueUrl, nil
	}()
	if err != nil {
		return fmt.Errorf("error when retrieving SQS queue URL: %v", err)
	}
	output, err := sqsClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: int32(1),
		WaitTimeSeconds:     int32(0),
	})
	if err != nil {
		return fmt.Errorf("error when receiving message from SQS queue: %v", err)
	}
	if len(output.Messages) == 0 {
		return nil
	}
	message := output.Messages[0]
	fmt.Println(*message.Body)
	if !delete {
		return nil
	}
	_, err = sqsClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      &queueURL,
		ReceiptHandle: message.ReceiptHandle,
	})
	if err != nil {
		return fmt.Errorf("error when deleting message from SQS queue: %v", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(receiveSqsMessageCmd)

	receiveSqsMessageCmd.Flags().String("region", defaultRegion, "Region of the SQS queue")
	receiveSqsMessageCmd.Flags().String("queue-arn", "", "ARN of the SQS queue")
	receiveSqsMessageCmd.MarkFlagRequired("queue-arn")
	receiveSqsMessageCmd.Flags().Bool("delete", false, "Delete received message from SQS queue")
}
