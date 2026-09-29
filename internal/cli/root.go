// SPDX-License-Identifier: BSD-3-Clause
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/naterator/litractl/internal/hidcmd"
	"github.com/naterator/litractl/internal/license"
	"github.com/naterator/litractl/internal/litra"
	"github.com/naterator/litractl/internal/update"
	"github.com/naterator/litractl/internal/usb"
	"github.com/spf13/cobra"
)

type updateFunc func(context.Context, string, bool) (update.Result, error)

func New(version string) *cobra.Command { return newCommand(version, usb.New, update.Run) }

func newCommand(version string, factory usb.Factory, updateRun updateFunc) *cobra.Command {
	var selector litra.Selector
	var dryRun, quiet bool
	root := &cobra.Command{
		Use: "litractl", Short: "Control USB-connected Logitech Litra Glow lights",
		Long: `Control all connected Litra Glow lights, or select one with --serial or --path.
Chain light commands to apply them in order.

Brightness presets: glow (10%), dim (20%), normal (40%), medium (60%),
                    bright (80%), brightest (100%).
Temperature presets: warmest (2700K), warm (3000K), mild (3500K),
                     neutral (4000K), cool (5000K), cold (5500K), coldest (6500K).
Zero percent is the hardware's minimum brightness; use off to switch off.`,
		Example: "  litractl on normal warm\n  litractl brightness 65 temperature 4500\n  litractl --serial SERIAL off\n  litractl --dry-run on brightest coldest",
		Version: version, SilenceUsage: true, SilenceErrors: true,
	}
	root.SetVersionTemplate("litractl {{.Version}}\n")
	root.PersistentFlags().StringVar(&selector.Serial, "serial", "", "Control only the light with this serial number")
	root.PersistentFlags().StringVar(&selector.Path, "path", "", "Control only the light with this HID path")
	root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Print light reports without opening USB devices")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress successful light-command output")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if cmd == root || cmd.Annotations["light"] == "true" {
			return nil
		}
		if dryRun {
			return fmt.Errorf("--dry-run applies only to light commands")
		}
		if cmd.Name() != "list" && (selector.Serial != "" || selector.Path != "" || quiet) {
			return fmt.Errorf("light selection and quiet flags do not apply to %s", cmd.CommandPath())
		}
		return nil
	}
	runLights := func(cmd *cobra.Command, args []string) error {
		actions, err := litra.Parse(args)
		if err != nil {
			return err
		}
		if dryRun {
			for _, a := range actions {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: % X\n", a.Description, a.Report)
			}
			return nil
		}
		return withBackend(factory, func(b usb.Backend) error {
			devices, err := litra.Devices(b, selector)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if quiet {
				out = io.Discard
			}
			return litra.Apply(cmd.Context(), b, devices, actions, out)
		})
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return runLights(cmd, args)
	}
	addLightCommand := func(name, usage, description string, aliases ...string) {
		cmd := &cobra.Command{Use: usage, Short: description, Aliases: aliases, Annotations: map[string]string{"light": "true"},
			RunE: func(cmd *cobra.Command, args []string) error { return runLights(cmd, append([]string{name}, args...)) },
		}
		root.AddCommand(cmd)
	}
	addLightCommand("on", "on [commands...]", "Turn the lights on")
	addLightCommand("off", "off [commands...]", "Turn the lights off")
	for _, preset := range litra.Presets {
		unit := "%"
		if preset.Kind == "temperature" {
			unit = "K"
		}
		addLightCommand(preset.Name, preset.Name+" [commands...]", fmt.Sprintf("Set %s to %d%s", preset.Kind, preset.Value, unit))
	}
	addLightCommand("brightness", "brightness PERCENT [commands...]", "Set brightness from 0 to 100 percent", "set_brightness")
	addLightCommand("temperature", "temperature KELVIN [commands...]", "Set white color temperature from 2700 to 6500K", "color", "colour", "set_temperature")
	root.AddCommand(&cobra.Command{Use: "set COMMAND [commands...]", Short: "Apply a sequence of light commands", RunE: runLights, Annotations: map[string]string{"light": "true"}})
	var listJSON bool
	list := &cobra.Command{Use: "list", Aliases: []string{"devices"}, Short: "List attached Litra Glow lights", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withBackend(factory, func(b usb.Backend) error {
				devices, err := litra.Devices(b, selector)
				if err != nil {
					return err
				}
				if listJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(devices)
				}
				if len(devices) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No matching Litra Glow lights found.")
				}
				for _, d := range devices {
					serial := d.Serial
					if serial == "" {
						serial = "(no serial)"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n  path: %s\n", serial, d.Product, d.Path)
				}
				return nil
			})
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "Print devices as JSON")
	root.AddCommand(list)
	root.AddCommand(&cobra.Command{Use: "hid [operations...]", Short: "Run low-level HID operations", Long: hidcmd.Help, DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || (len(args) == 1 && (args[0] == "--help" || args[0] == "-h")) {
				fmt.Fprint(cmd.OutOrStdout(), hidcmd.Help)
				return nil
			}
			program, err := hidcmd.Parse(args)
			if err != nil {
				return err
			}
			return withBackend(factory, func(b usb.Backend) error { return program.Run(cmd.Context(), b, cmd.OutOrStdout(), version) })
		},
	})
	root.AddCommand(&cobra.Command{Use: "version", Short: "Print the program version", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) { fmt.Fprintf(cmd.OutOrStdout(), "litractl %s\n", version) },
	})
	root.AddCommand(&cobra.Command{Use: "license", Short: "Print the project license", Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), license.License)
		},
	})
	root.AddCommand(newUpdateCommand(version, updateRun))
	return root
}

func withBackend(factory usb.Factory, fn func(usb.Backend) error) (result error) {
	b, err := factory()
	if err != nil {
		return fmt.Errorf("initialize USB HID: %w", err)
	}
	defer func() { result = errors.Join(result, b.Close()) }()
	return fn(b)
}
