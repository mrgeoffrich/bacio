package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/invopop/jsonschema"
	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/schema"
)

func newSchemaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Publish JSON Schemas for every mutating command's --json payload",
		Long: `Runtime schema introspection for agents driving bacio via --json.

Each schema describes the JSON shape one mutating command accepts on its
--json flag. Schema names are dotted forms of the cobra command path
(e.g. "bacio issue add" → "issue.add"). The schema set here mirrors the
strict decoder, so any payload that matches the schema will decode
without "unknown field" errors.`,
	}
	cmd.AddCommand(schemaListCmd(), schemaShowCmd(), schemaAllCmd())
	return cmd
}

func schemaListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every command name with a published JSON-input schema",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.output == outputJSON {
				type row struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}
				out := make([]row, 0, len(schema.Registry))
				for _, e := range schema.Registry {
					out = append(out, row{e.Name, e.Short})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			width := 0
			for _, e := range schema.Registry {
				if len(e.Name) > width {
					width = len(e.Name)
				}
			}
			for _, e := range schema.Registry {
				fmt.Fprintf(os.Stdout, "%-*s  %s\n", width, e.Name, e.Short)
			}
			return nil
		},
	}
}

func schemaShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print the JSON Schema for a single command (e.g. \"issue.add\")",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			entry, ok := schema.Find(args[0])
			if !ok {
				return fmt.Errorf("unknown schema %q; run `bacio schema list` to see all names", args[0])
			}
			s := schema.Build(entry)
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(s)
		},
	}
}

func schemaAllCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "all",
		Short: "Print every published schema as one JSON object keyed by command name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := make(map[string]*jsonschema.Schema, len(schema.Registry))
			names := make([]string, 0, len(schema.Registry))
			for _, e := range schema.Registry {
				out[e.Name] = schema.Build(e)
				names = append(names, e.Name)
			}
			sort.Strings(names)
			fmt.Fprint(os.Stdout, "{\n")
			for i, name := range names {
				body, err := json.MarshalIndent(out[name], "  ", "  ")
				if err != nil {
					return err
				}
				sep := ","
				if i == len(names)-1 {
					sep = ""
				}
				fmt.Fprintf(os.Stdout, "  %q: %s%s\n", name, body, sep)
			}
			fmt.Fprint(os.Stdout, "}\n")
			return nil
		},
	}
}
