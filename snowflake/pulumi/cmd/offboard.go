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
		workdir, _ := cmd.Flags().GetString("workdir")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		mgr := stack.NewManager(logger, stackName, bucketName, region, "", workdir)
		return mgr.Offboard(ctx)
	},
}

func init() {
	rootCmd.AddCommand(offboardCmd)

	offboardCmd.Flags().String("region", defaultRegion, "Region where bucket should be created")
	offboardCmd.Flags().String("stack-name", "default", "Name of the infrastructure stack: useful to deploy multiple instances in the same Snowflake account or with the same bucket (e.g., one for dev and one for prod)")
	offboardCmd.Flags().String("bucket-name", "", "Bucket to store infra state and Antrea flows")
	offboardCmd.MarkFlagRequired("bucket-name")
	offboardCmd.Flags().String("workdir", "", "Use provided local workdir (by default a temporary one will be created")
}
