/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var verbosity int

var logger logr.Logger

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "theia-sf",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if verbosity < 0 || verbosity >= 128 {
			return fmt.Errorf("invalid verbosity level %d: it should be >= 0 and < 128", verbosity)
		}
		zc := zap.NewProductionConfig()
		zc.Level = zap.NewAtomicLevelAt(zapcore.Level(-1 * verbosity))
		zc.DisableStacktrace = true
		zapLog, err := zc.Build()
		if err != nil {
			return fmt.Errorf("cannot initialize Zap logger: %w", err)
			panic("Cannot initialize Zap logger")
		}
		logger = zapr.NewLogger(zapLog)
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rand.Seed(time.Now().UnixNano())

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().IntVarP(&verbosity, "verbosity", "v", 0, "log verbosity")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	// rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
