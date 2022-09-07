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
		bucketName, _ := cmd.Flags().GetString("bucket-name")
		workdir, _ := cmd.Flags().GetString("workdir")
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		mgr := stack.NewManager(logger, bucketName, region, workdir)
		return mgr.Onboard(ctx)
	},
}

func init() {
	rootCmd.AddCommand(onboardCmd)

	onboardCmd.Flags().String("region", defaultRegion, "Region where bucket should be created")
	onboardCmd.Flags().String("bucket-name", "", "Bucket to store infra state and Antrea flows")
	onboardCmd.MarkFlagRequired("bucket-name")
	onboardCmd.Flags().String("workdir", "", "Use provided local workdir (by default a temporary one will be created")
}
