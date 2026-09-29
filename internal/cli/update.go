// SPDX-License-Identifier: BSD-3-Clause
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newUpdateCommand(version string, run updateFunc) *cobra.Command {
	var checkOnly bool
	execute := func(cmd *cobra.Command, check bool) error {
		result, err := run(cmd.Context(), version, check)
		if err != nil {
			return err
		}
		switch {
		case result.Updated:
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s to %s at %s\n", result.Current, result.Latest, result.Path)
			if result.Backup != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Previous executable retained at %s until it is no longer running.\n", result.Backup)
			}
		case result.Available:
			fmt.Fprintf(cmd.OutOrStdout(), "Update available: %s -> %s. Run litractl update to install.\n", result.Current, result.Latest)
		default:
			fmt.Fprintf(cmd.OutOrStdout(), "litractl %s is up to date (latest: %s).\n", result.Current, result.Latest)
		}
		return nil
	}
	cmd := &cobra.Command{Use: "update", Short: "Install the latest stable GitHub release", Args: cobra.NoArgs,
		Long: "Check GitHub for a stable release, verify its SHA-256 checksum and Go binary identity, and replace this executable. Use --check for a read-only check.",
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, checkOnly) },
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for an update without downloading or installing it")
	cmd.AddCommand(&cobra.Command{Use: "check", Short: "Check for an update without installing it", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return execute(cmd, true) },
	})
	return cmd
}
