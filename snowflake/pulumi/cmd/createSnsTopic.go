/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/spf13/cobra"

	snsclient "antrea.io/theia/snowflake/pulumi/pkg/aws/client/sns"
)

// createSNSTopicCmd represents the create-sns-topic command
var createSNSTopicCmd = &cobra.Command{
	Use:   "create-sns-topic",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		region, _ := cmd.Flags().GetString("region")
		snsTopicName, _ := cmd.Flags().GetString("name")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
		if err != nil {
			return fmt.Errorf("unable to load AWS SDK config: %w", err)

		}
		snsClient := snsclient.GetClient(awsCfg)
		snsTopicARN, err := createSNSTopic(ctx, snsClient, snsTopicName, region)
		if err != nil {
			return err
		}
		fmt.Printf("SNS Topic created successfully with ARN: %s\n", snsTopicARN)
		return nil
	},
}

func createSNSTopic(ctx context.Context, snsClient snsclient.Interface, name string, region string) (string, error) {
	logger := logger.WithValues("name", name)
	logger.Info("Creating SNS topic")
	// CreateTopic is idempotent
	output, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: &name,
	})
	if err != nil {
		return "", fmt.Errorf("error when creating SNS topic '%s': %w", name, err)
	}
	logger.Info("Created SNS topic")
	return *output.TopicArn, nil
}

func init() {
	rootCmd.AddCommand(createSNSTopicCmd)
	createSNSTopicCmd.Flags().String("region", defaultRegion, "Region where SNS Topic should be created")
	createSNSTopicCmd.Flags().String("name", "", "Name of SNS Topic to create")
	createSNSTopicCmd.MarkFlagRequired("name")
}
