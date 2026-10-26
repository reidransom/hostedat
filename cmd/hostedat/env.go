package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func envCmd() *cobra.Command {
	group := &cobra.Command{Use: "env", Short: "Check local or remote secret declarations without changing values"}
	group.AddCommand(envCheckCmd())
	return group
}

func envCheckCmd() *cobra.Command {
	var schema, site string
	var remote, insecure bool
	cmd := &cobra.Command{Use: "check", Short: "Validate local .env plus shell overrides, or stored remote secrets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if remote && site == "" {
			return errors.New("remote environment check requires --site")
		}
		if !remote && (cmd.Flags().Changed("site") || cmd.Flags().Changed("allow-insecure-loopback")) {
			return errors.New("--site and --allow-insecure-loopback require --remote")
		}
		project, err := secretProject(schema)
		if err != nil {
			return err
		}
		if project.Schema == nil {
			return errors.New("environment check requires env.json, env.yml, or --schema")
		}
		if !remote {
			values, err := project.LocalValues(os.LookupEnv)
			if err != nil {
				return err
			}
			if err := project.Schema.Validate(values); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Local environment valid (%d declarations).\n", len(project.Schema))
			return err
		}
		// Do not read dotenv or shell values in remote mode. The typed client
		// serializes only the parsed declaration and never mutates stored values.
		c, target, err := secretTarget(site, insecure)
		if err != nil {
			return err
		}
		result, err := c.ValidateEnvVars(target.ID, project.Schema)
		if err != nil {
			return err
		}
		if !result.Valid {
			return errors.New("remote environment validation failed")
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Remote environment valid for site %q (%d declarations).\n", target.ID, len(project.Schema))
		return err
	}}
	cmd.Flags().StringVar(&schema, "schema", "", "declaration file (.json or .yml)")
	cmd.Flags().BoolVar(&remote, "remote", false, "validate stored target secrets without reading local values")
	cmd.Flags().StringVar(&site, "site", "", "site ID, name, or slug (required with --remote)")
	cmd.Flags().BoolVar(&insecure, "allow-insecure-loopback", false, "allow HTTP only to a loopback development server (requires --remote)")
	return cmd
}
