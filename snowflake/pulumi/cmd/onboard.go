/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"antrea.io/theia/snowflake/pulumi/pkg/stack"
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
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		if bucketRegion == "" {
			var err error
			bucketRegion, err = GetBucketRegion(ctx, bucketName, region)
			if err != nil {
				return err
			}
		}
		mgr := stack.NewManager(logger, stackName, bucketName, bucketRegion, region, warehouseName, workdir)
		return mgr.Onboard(ctx)
	},
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
}
