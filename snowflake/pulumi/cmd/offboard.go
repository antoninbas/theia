/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"antrea.io/theia/snowflake/pulumi/pkg/infra"
)

// offboardCmd represents the offboard command
var offboardCmd = &cobra.Command{
	Use:   "offboard",
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
		mgr := infra.NewManager(logger, stackName, bucketName, bucketRegion, region, "", workdir, verbose)
		if err := mgr.Offboard(ctx); err != nil {
			return err
		}
		fmt.Println("SUCCESS!")
		fmt.Println("To re-create infrastructure, run 'theia-sf onboard' again")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(offboardCmd)

	offboardCmd.Flags().String("region", defaultRegion, "Region where bucket should be created")
	offboardCmd.Flags().String("stack-name", "default", "Name of the infrastructure stack: useful to deploy multiple instances in the same Snowflake account or with the same bucket (e.g., one for dev and one for prod)")
	offboardCmd.Flags().String("bucket-name", "", "Bucket to store infra state and Antrea flows")
	offboardCmd.MarkFlagRequired("bucket-name")
	offboardCmd.Flags().String("bucket-region", "", "Region where infra bucket is defined; if omitted, we will try to get the region from AWS")
	offboardCmd.Flags().String("workdir", "", "Use provided local workdir (by default a temporary one will be created")
	offboardCmd.Flags().Bool("verbose", false, "Output detailed information to stdout about infrastructure provisioning")
}
