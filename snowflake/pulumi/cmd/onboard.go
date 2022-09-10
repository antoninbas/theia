/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"

	"antrea.io/theia/snowflake/pulumi/pkg/infra"
)

// onboardCmd represents the onboard command
var onboardCmd = &cobra.Command{
	Use:   "onboard",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		region, _ := cmd.Flags().GetString("region")
		stackName, _ := cmd.Flags().GetString("stack-name")
		bucketName, _ := cmd.Flags().GetString("bucket-name")
		bucketRegion, _ := cmd.Flags().GetString("bucket-region")
		warehouseName, _ := cmd.Flags().GetString("warehouse-name")
		workdir, _ := cmd.Flags().GetString("workdir")
		verbose, _ := cmd.Flags().GetBool("verbose")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		if bucketRegion == "" {
			var err error
			bucketRegion, err = GetBucketRegion(ctx, bucketName, region)
			if err != nil {
				return err
			}
		}
		mgr := infra.NewManager(logger, stackName, bucketName, bucketRegion, region, warehouseName, workdir, verbose)
		result, err := mgr.Onboard(ctx)
		if err != nil {
			return err
		}
		showResults(result)
		fmt.Println("SUCCESS!")
		fmt.Println("To update infrastructure, run 'theia-sf onboard' again")
		fmt.Println("To destroy all infrastructure, run 'theia-sf offboard'")
		return nil
	},
}

func showResults(result *infra.Result) {
	table := tablewriter.NewWriter(os.Stdout)
	data := [][]string{
		[]string{"Region", result.Region},
		[]string{"Bucket Name", result.BucketName},
		[]string{"Bucket Flows Folder", result.BucketFlowsFolder},
		[]string{"Snowflake Database Name", result.DatabaseName},
		[]string{"Snowflake Schema Name", result.SchemaName},
		[]string{"Snowflake Flows Table Name", result.FlowsTableName},
		[]string{"SNS Topic ARN", result.SNSTopicARN},
		[]string{"SQS Queue ARN", result.SQSQueueARN},
	}
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.AppendBulk(data)
	table.Render()
}

func init() {
	rootCmd.AddCommand(onboardCmd)

	onboardCmd.Flags().String("region", defaultRegion, "Region where AWS resources will be provisioned")
	onboardCmd.Flags().String("stack-name", "default", "Name of the infrastructure stack: useful to deploy multiple instances in the same Snowflake account or with the same bucket (e.g., one for dev and one for prod)")
	onboardCmd.Flags().String("bucket-name", "", "Bucket to store infra state and Antrea flows")
	onboardCmd.MarkFlagRequired("bucket-name")
	onboardCmd.Flags().String("bucket-region", "", "Region where infra bucket is defined; if omitted, we will try to get the region from AWS")
	onboardCmd.Flags().String("workdir", "", "Use provided local workdir (by default a temporary one will be created")
	onboardCmd.Flags().String("warehouse-name", "", "Snowflake Virtual Warehouse to use for onboarding queries, by default we will use a temporary one")
	onboardCmd.Flags().Bool("verbose", false, "Output detailed information to stdout about infrastructure provisioning")
}
