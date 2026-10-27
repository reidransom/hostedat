package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/cryguy/hostedat/internal/client"
	"github.com/cryguy/hostedat/internal/environment"
	"github.com/cryguy/hostedat/internal/envschema"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func secretCmd() *cobra.Command {
	var insecure bool
	group := &cobra.Command{Use: "secret", Short: "Manage server-side secrets without revealing values"}
	group.PersistentFlags().BoolVar(&insecure, "allow-insecure-loopback", false, "allow HTTP only to a loopback development server")
	group.AddCommand(secretPutCmd(&insecure), secretSyncCmd(&insecure), secretListCmd(&insecure), secretDeleteCmd(&insecure))
	return group
}

// Resolve credentials, transport support and the existing site reference before
// reading a credential or prompting. No saved target or local dotenv is read.
func secretTarget(ref string, insecure bool) (*client.Client, *client.Site, error) {
	c, err := newClient()
	if err != nil {
		return nil, nil, err
	}
	c.AllowInsecureLoopback = insecure
	if err := c.RequireSecretsCapability(); err != nil {
		return nil, nil, err
	}
	site, err := c.ResolveSite(ref)
	if err != nil {
		return nil, nil, err
	}
	return c, site, nil
}

func secretProject(schema string) (*envschema.Project, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, errors.New("cannot resolve invocation directory")
	}
	return envschema.Discover(dir, schema)
}

func validateSecret(name, value string, schema envschema.Schema) error {
	if err := envschema.ValidateName(name); err != nil {
		return err
	}
	if err := envschema.ValidateRuntimeName(name); err != nil {
		return err
	}
	if len(value) > environment.MaxValueBytes {
		return errors.New("secret exceeds value size limit")
	}
	if !utf8.ValidString(value) {
		return errors.New("secret must be UTF-8")
	}
	if schema != nil {
		return schema.ValidateValue(name, value, true)
	}
	return nil
}

func secretPutCmd(insecure *bool) *cobra.Command {
	var stdin bool
	var schema string
	cmd := &cobra.Command{Use: "put <site> <name>", Short: "Create or replace one secret using hidden input or explicit stdin", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if err := envschema.ValidateName(args[1]); err != nil {
			return err
		}
		if err := envschema.ValidateRuntimeName(args[1]); err != nil {
			return err
		}
		project, err := secretProject(schema)
		if err != nil {
			return err
		}
		if project.Schema != nil {
			if _, ok := project.Schema[args[1]]; !ok {
				return fmt.Errorf("environment variable %s: not declared", args[1])
			}
		}
		c, site, err := secretTarget(args[0], *insecure)
		if err != nil {
			return err
		}
		// Quoted fields prevent control characters in server metadata affecting the terminal.
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Target: %q site %q (%q)\n", c.BaseURL, site.Name, site.ID); err != nil {
			return err
		}
		value, err := readSecret(cmd, stdin)
		if err != nil {
			return err
		}
		if err := validateSecret(args[1], value, project.Schema); err != nil {
			return err
		}
		if _, err := c.SetEnvVar(site.ID, client.EnvWrite{Name: args[1], Value: value, Secret: true}, project.Schema); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Secret %s saved.\n", args[1])
		return err
	}}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the secret from stdin (required without a terminal)")
	cmd.Flags().StringVar(&schema, "schema", "", "declaration file (.json or .yml)")
	return cmd
}

func readSecret(cmd *cobra.Command, stdin bool) (string, error) {
	input := cmd.InOrStdin()
	if stdin {
		data, err := io.ReadAll(io.LimitReader(input, environment.MaxValueBytes+3))
		if err != nil {
			return "", errors.New("cannot read secret input")
		}
		if len(data) > environment.MaxValueBytes+2 {
			return "", errors.New("secret exceeds value size limit")
		}
		value := strings.TrimSuffix(string(data), "\n")
		if len(value) != len(data) {
			value = strings.TrimSuffix(value, "\r")
		}
		if len(value) > environment.MaxValueBytes {
			return "", errors.New("secret exceeds value size limit")
		}
		return value, nil
	}
	file, ok := input.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", errors.New("secret input is not a terminal; use --stdin explicitly")
	}
	if _, err := fmt.Fprint(cmd.ErrOrStderr(), "Secret (hidden): "); err != nil {
		return "", err
	}
	// ReadPassword buffers without a bound. Raw mode allows bounded hidden input
	// and deterministic restoration, including cancellation and size-limit errors.
	state, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return "", errors.New("cannot hide terminal input")
	}
	defer term.Restore(int(file.Fd()), state)
	defer fmt.Fprintln(cmd.ErrOrStderr())
	var data []byte
	var one [1]byte
	for {
		n, err := input.Read(one[:])
		if n != 0 {
			switch one[0] {
			case '\r', '\n':
				return string(data), nil
			case 3, 4:
				return "", errors.New("secret input cancelled")
			case 8, 127:
				if len(data) != 0 {
					_, size := utf8.DecodeLastRune(data)
					data = data[:len(data)-size]
				}
			default:
				if len(data) == environment.MaxValueBytes {
					return "", errors.New("secret exceeds value size limit")
				}
				data = append(data, one[0])
			}
		}
		if err != nil {
			return "", errors.New("cannot read secret input")
		}
	}
}

func secretSyncCmd(insecure *bool) *cobra.Command {
	var schema string
	cmd := &cobra.Command{Use: "sync <site> <file>", Short: "Atomically merge a named dotenv file (never the project's local .env)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		project, err := secretProject(schema)
		if err != nil {
			return err
		}
		values, err := project.ReadSyncSource(args[1])
		if err != nil {
			return err
		}
		if len(values) == 0 || len(values) > environment.MaxBatchEntries {
			return errors.New("sync requires 1 to 256 entries")
		}
		names := make([]string, 0, len(values))
		total := 0
		for name, value := range values {
			if err := validateSecret(name, value, project.Schema); err != nil {
				return err
			}
			total += len(name) + len(value)
			names = append(names, name)
		}
		if total > environment.MaxBatchValueBytes {
			return errors.New("sync exceeds batch size limit")
		}
		sort.Strings(names)
		entries := make([]client.EnvWrite, 0, len(names))
		for _, name := range names {
			entries = append(entries, client.EnvWrite{Name: name, Value: values[name], Secret: true})
		}
		c, site, err := secretTarget(args[0], *insecure)
		if err != nil {
			return err
		}
		if _, err := c.BulkSetEnvVars(site.ID, entries, project.Schema); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Synced %d secrets: %s.\n", len(names), strings.Join(names, ", "))
		return err
	}}
	cmd.Flags().StringVar(&schema, "schema", "", "declaration file (.json or .yml)")
	return cmd
}

type secretMetadata struct {
	ID     string `json:"id"`
	SiteID string `json:"site_id"`
	Name   string `json:"name"`
	Secret bool   `json:"secret"`
}

func secretListCmd(insecure *bool) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{Use: "list <site>", Short: "List sorted secret metadata only", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, site, err := secretTarget(args[0], *insecure)
		if err != nil {
			return err
		}
		vars, err := c.ListEnvVars(site.ID)
		if err != nil {
			return err
		}
		entries := make([]secretMetadata, 0, len(vars))
		for _, v := range vars {
			if v.Secret {
				entries = append(entries, secretMetadata{v.ID, v.SiteID, v.Name, true})
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		if jsonOutput {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "NAME\tID\tSECRET"); err != nil {
			return err
		}
		for _, entry := range entries {
			if _, err := fmt.Fprintf(w, "%s\t%s\ttrue\n", entry.Name, entry.ID); err != nil {
				return err
			}
		}
		return w.Flush()
	}}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print metadata as JSON (never values)")
	return cmd
}

func secretDeleteCmd(insecure *bool) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{Use: "delete <site> <name>", Short: "Delete a site-scoped secret by name", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		if err := envschema.ValidateName(args[1]); err != nil {
			return err
		}
		c, site, err := secretTarget(args[0], *insecure)
		if err != nil {
			return err
		}
		vars, err := c.ListEnvVars(site.ID)
		if err != nil {
			return err
		}
		var selected *client.EnvVar
		for i := range vars {
			if vars[i].Name == args[1] {
				if selected != nil {
					return errors.New("duplicate environment name; operator resolution required")
				}
				selected = &vars[i]
			}
		}
		if selected == nil {
			return fmt.Errorf("secret %s not found in selected site", args[1])
		}
		if !selected.Secret {
			return errors.New("refusing to delete a plaintext binding with a secret command")
		}
		if !yes {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Delete secret %s from site %q (%q)? [y/N] ", args[1], site.Name, site.ID); err != nil {
				return err
			}
			answer, err := bufio.NewReader(io.LimitReader(cmd.InOrStdin(), 1024)).ReadString('\n')
			if err != nil && err != io.EOF {
				return errors.New("cannot read confirmation")
			}
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return err
			}
		}
		if err := c.DeleteEnvVar(site.ID, selected.ID); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Secret %s deleted.\n", args[1])
		return err
	}}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}
