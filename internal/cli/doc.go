package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/cli/inputs"
	"github.com/mrgeoffrich/bacio/internal/client"
	"github.com/mrgeoffrich/bacio/internal/inputio"
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

var issueKeyShape = regexp.MustCompile(`^[A-Za-z0-9]{4}-\d+$`)

// isIssueKey reports whether s has the PREFIX-N shape, used to disambiguate
// "issue or feature?" positionals on commands like `bacio doc link`.
func isIssueKey(s string) bool { return issueKeyShape.MatchString(s) }

// validateDocFilename runs the strict store-layer validator. Kept as a
// CLI-side wrapper so every entry point (positional flag path, JSON path,
// rename) goes through the same rules and surfaces errors at parse time
// instead of waiting for the SQL write. No silent trimming — leading or
// trailing whitespace is a hard error so an agent that fat-fingered a
// payload sees the problem instead of having it normalised away.
func validateDocFilename(name string) (string, error) {
	return store.ValidateDocFilenameStrict(name)
}

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "doc", Short: "Manage per-repo text documents and their links to issues/features"}
	cmd.AddCommand(
		docAddCmd(), docUpsertCmd(), docListCmd(), docShowCmd(),
		docEditCmd(), docRenameCmd(), docExportCmd(), docDownloadCmd(), docRmCmd(),
		docLinkCmd(), docUnlinkCmd(),
	)
	return cmd
}

// docInputs is the resolved (filename, type, body, source_path) tuple that
// `add` and `upsert` operate on, after merging positional / --from-path /
// explicit flags. SourcePath is the value that should be stored on the row;
// it's set when --from-path was used and empty otherwise.
type docInputs struct {
	Filename   string
	Type       model.DocumentType
	Body       string
	SourcePath string
}

// resolveDocInputs derives the filename, type, and content body from the CLI
// surface shared by `bacio doc add` and `bacio doc upsert`. Explicit flags always
// win over values derived from --from-path; --from-path supplies sensible
// defaults so skills don't have to do path-to-filename translation by hand.
func resolveDocInputs(positional, fromPath, typeStr, content, contentFile string) (*docInputs, error) {
	if positional != "" && fromPath != "" {
		return nil, fmt.Errorf("provide a filename positional OR --from-path, not both")
	}

	var (
		filename string
		err      error
	)
	switch {
	case positional != "":
		filename, err = validateDocFilename(positional)
	case fromPath != "":
		filename, err = validateDocFilename(canonicalDocFilename(fromPath))
	default:
		return nil, fmt.Errorf("provide a filename positional or --from-path")
	}
	if err != nil {
		return nil, err
	}

	var t model.DocumentType
	switch {
	case typeStr != "":
		t, err = model.ParseDocumentType(typeStr)
		if err != nil {
			return nil, err
		}
	case fromPath != "":
		derived, ok := deriveDocTypeFromPath(fromPath)
		if !ok {
			return nil, fmt.Errorf("--type required: cannot derive document type from path %q", fromPath)
		}
		t = derived
	default:
		return nil, fmt.Errorf("--type is required")
	}

	contentFileEffective := contentFile
	if content == "" && contentFile == "" {
		if fromPath == "" {
			return nil, fmt.Errorf("provide --content - (stdin) or --content-file <path>")
		}
		contentFileEffective = fromPath
	}
	body, err := readLongText(content, contentFileEffective, true, "content")
	if err != nil {
		return nil, err
	}
	if !utf8.ValidString(body) {
		return nil, fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
	}

	sourcePath := ""
	if fromPath != "" {
		sourcePath = canonicalSourcePath(fromPath)
		if err := validateRelativePath(sourcePath); err != nil {
			return nil, fmt.Errorf("--from-path: %w", err)
		}
	}
	return &docInputs{Filename: filename, Type: t, Body: body, SourcePath: sourcePath}, nil
}

// resolveDocInputsJSON is the JSON-side counterpart of resolveDocInputs.
// Unlike the flag path, it never reads from disk: content must be supplied
// inline. Either filename or source_path is required (not both); type is
// required unless source_path's directory implies one.
func resolveDocInputsJSON(filename, sourcePath, typeStr, content string) (*docInputs, error) {
	if filename != "" && sourcePath != "" {
		return nil, fmt.Errorf("provide filename OR source_path, not both")
	}
	var (
		fname string
		err   error
	)
	switch {
	case filename != "":
		fname, err = validateDocFilename(filename)
	case sourcePath != "":
		fname, err = validateDocFilename(canonicalDocFilename(sourcePath))
	default:
		return nil, fmt.Errorf("provide filename or source_path")
	}
	if err != nil {
		return nil, err
	}

	var t model.DocumentType
	switch {
	case typeStr != "":
		t, err = model.ParseDocumentType(typeStr)
		if err != nil {
			return nil, err
		}
	case sourcePath != "":
		derived, ok := deriveDocTypeFromPath(sourcePath)
		if !ok {
			return nil, fmt.Errorf("type is required: cannot derive document type from source_path %q", sourcePath)
		}
		t = derived
	default:
		return nil, fmt.Errorf("type is required")
	}

	if content == "" {
		return nil, fmt.Errorf("content is required")
	}
	if !utf8.ValidString(content) {
		return nil, fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
	}

	sp := ""
	if sourcePath != "" {
		sp = canonicalSourcePath(sourcePath)
		if err := validateRelativePath(sp); err != nil {
			return nil, fmt.Errorf("source_path: %w", err)
		}
	}
	return &docInputs{Filename: fname, Type: t, Body: content, SourcePath: sp}, nil
}

// canonicalSourcePath normalises a --from-path value to its repo-relative
// form: trims whitespace, leading "./" / "/", and forward-slash separators.
func canonicalSourcePath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return p
}

// validateRelativePath rejects paths that escape the repo root or are
// absolute. Used both for --from-path on import and for the resolved
// destination on `bacio doc export`.
func validateRelativePath(p string) error {
	if p == "" {
		return fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return fmt.Errorf("path %q must be relative", p)
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path %q escapes the repo root", p)
	}
	return nil
}

// canonicalDocFilename converts a repo-relative path like
// "docs/planning/not-shipped/foo.md" into "docs-planning-not-shipped-foo.md".
func canonicalDocFilename(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return strings.ReplaceAll(p, "/", "-")
}

// deriveDocTypeFromPath maps a small set of directory conventions to a
// document type. Returns (_, false) when no convention matches and the caller
// must require an explicit --type.
func deriveDocTypeFromPath(p string) (model.DocumentType, bool) {
	p = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "./"))
	switch {
	case strings.HasPrefix(p, "docs/planning/not-shipped/"):
		return model.DocTypeProjectInPlanning, true
	case strings.HasPrefix(p, "docs/planning/in-progress/"):
		return model.DocTypeProjectInProgress, true
	case strings.HasPrefix(p, "docs/planning/shipped/"):
		return model.DocTypeProjectComplete, true
	}
	return "", false
}

func docAddCmd() *cobra.Command {
	var typeStr, content, contentFile, fromPath, rawInput string
	cmd := &cobra.Command{
		Use:   "add [filename]",
		Short: "Create a document in the current repo",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"type", "content", "content-file", "from-path")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
				if err != nil {
					return err
				}
				resolved, err := resolveDocInputsJSON(in.Filename, in.SourcePath, in.Type, in.Content)
				if err != nil {
					return err
				}
				return createDocument(resolved)
			}
			if cmd.Flags().Changed("from-path") && inRemoteMode() {
				return fmt.Errorf("--from-path is not supported in remote mode (the API cannot read the client's filesystem); pass --content/--content-file with the filename positional instead")
			}
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			resolved, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
			}
			return createDocument(resolved)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path (local mode only)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func createDocument(in *docInputs) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	d, err := c.CreateDocument(context.Background(), repo, client.DocCreateInput{
		Filename:   in.Filename,
		Type:       in.Type,
		Body:       in.Body,
		SourcePath: in.SourcePath,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(d)
	}
	if opts.output == outputJSON {
		return emit(d)
	}
	return ok("Created %s in %s (type=%s, %d bytes)",
		d.Filename, repo.Prefix, d.Type, d.SizeBytes)
}

func docUpsertCmd() *cobra.Command {
	var typeStr, content, contentFile, fromPath, rawInput string
	cmd := &cobra.Command{
		Use:   "upsert [filename]",
		Short: "Create or update a document (same flag surface as `add`)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"type", "content", "content-file", "from-path")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocAddInput](raw)
				if err != nil {
					return err
				}
				resolved, err := resolveDocInputsJSON(in.Filename, in.SourcePath, in.Type, in.Content)
				if err != nil {
					return err
				}
				return upsertDocument(resolved)
			}
			if cmd.Flags().Changed("from-path") && inRemoteMode() {
				return fmt.Errorf("--from-path is not supported in remote mode (the API cannot read the client's filesystem); pass --content/--content-file with the filename positional instead")
			}
			pos := ""
			if len(args) == 1 {
				pos = args[0]
			}
			resolved, err := resolveDocInputs(pos, fromPath, typeStr, content, contentFile)
			if err != nil {
				return err
			}
			return upsertDocument(resolved)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "document type (e.g. architecture, designs, user-docs)")
	cmd.Flags().StringVar(&content, "content", "", "content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	cmd.Flags().StringVar(&fromPath, "from-path", "", "derive filename (and optionally type+content) from a repo-relative path (local mode only)")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func upsertDocument(in *docInputs) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	d, err := c.UpsertDocument(context.Background(), repo, client.DocCreateInput{
		Filename:   in.Filename,
		Type:       in.Type,
		Body:       in.Body,
		SourcePath: in.SourcePath,
	}, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(d)
	}
	if opts.output == outputJSON {
		return emit(d)
	}
	return ok("Updated %s in %s (type=%s, %d bytes)",
		d.Filename, repo.Prefix, d.Type, d.SizeBytes)
}

func docListCmd() *cobra.Command {
	var typeStr string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List documents in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			docs, err := c.ListDocuments(context.Background(), repo, typeStr)
			if err != nil {
				return err
			}
			return emit(docs)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "filter by type")
	return cmd
}

func docShowCmd() *cobra.Command {
	var raw, metadata bool
	cmd := &cobra.Command{
		Use:   "show <filename>",
		Short: "Show a document's metadata, content, and links",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if raw && metadata {
				return fmt.Errorf("--raw and --metadata are mutually exclusive")
			}
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			view, err := retryWithRedirect(repo, "document", args[0],
				func(filename string) (*client.DocView, error) {
					return c.ShowDocument(context.Background(), repo, filename, !metadata)
				})
			if err != nil {
				return err
			}
			if raw {
				_, err := os.Stdout.WriteString(view.Document.Content)
				return err
			}
			return emit(&docView{Document: view.Document, Links: view.Links})
		},
	}
	cmd.Flags().BoolVar(&raw, "raw", false, "write content to stdout with no metadata (ignores --output)")
	cmd.Flags().BoolVar(&metadata, "metadata", false, "skip the document body (returns type, size, links, and timestamps only)")
	return cmd
}

func docEditCmd() *cobra.Command {
	var (
		typeStr, content, contentFile, rawInput string
	)
	cmd := &cobra.Command{
		Use:   "edit [filename]",
		Short: "Edit a document's type and/or content",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput,
				"type", "content", "content-file")
			if err != nil {
				return err
			}
			if raw != nil {
				in, present, err := inputio.DecodeStrict[inputs.DocEditInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				var (
					newType    *string
					newContent *string
				)
				if _, ok := present["type"]; ok {
					if in.Type == nil || *in.Type == "" {
						return fmt.Errorf("type cannot be empty or null; omit the field to leave it unchanged")
					}
					newType = in.Type
				}
				if _, ok := present["content"]; ok {
					body := ""
					if in.Content != nil {
						body = *in.Content
					}
					if !utf8.ValidString(body) {
						return fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
					}
					newContent = &body
				}
				return applyDocEdit(in.Filename, newType, newContent)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			var (
				newType    *string
				newContent *string
			)
			if typeStr != "" {
				newType = &typeStr
			}
			if content != "" || contentFile != "" {
				body, err := readLongText(content, contentFile, true, "content")
				if err != nil {
					return err
				}
				if !utf8.ValidString(body) {
					return fmt.Errorf("document is not valid UTF-8 text; only text documents are supported")
				}
				newContent = &body
			}
			return applyDocEdit(args[0], newType, newContent)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "new type")
	cmd.Flags().StringVar(&content, "content", "", "new content text or '-' for stdin")
	cmd.Flags().StringVar(&contentFile, "content-file", "", "path to a markdown file")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func applyDocEdit(filename string, newType *string, newContent *string) error {
	if newType == nil && newContent == nil {
		return fmt.Errorf("nothing to update; pass type and/or content")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	updated, err := c.EditDocument(context.Background(), repo, filename, newType, newContent, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	return emit(updated)
}

func docRenameCmd() *cobra.Command {
	var typeStr, rawInput string
	cmd := &cobra.Command{
		Use:   "rename [old-filename] [new-filename]",
		Short: "Rename a document, preserving its links (and optionally update its type)",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "type")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocRenameInput](raw)
				if err != nil {
					return err
				}
				if in.OldFilename == "" || in.NewFilename == "" {
					return fmt.Errorf("old_filename and new_filename are required")
				}
				return renameDocument(in.OldFilename, in.NewFilename, in.Type)
			}
			if len(args) != 2 {
				return fmt.Errorf("requires <old-filename> <new-filename> positionals or --json")
			}
			return renameDocument(args[0], args[1], typeStr)
		},
	}
	cmd.Flags().StringVar(&typeStr, "type", "", "optionally also update the document type")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func renameDocument(oldArg, newArg, typeStr string) error {
	oldName, err := validateDocFilename(oldArg)
	if err != nil {
		return err
	}
	newName, err := validateDocFilename(newArg)
	if err != nil {
		return err
	}
	if oldName == newName && typeStr == "" {
		return fmt.Errorf("nothing to rename: old and new filenames are identical")
	}
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	updated, err := c.RenameDocument(context.Background(), repo, oldName, newName, typeStr, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(updated)
	}
	if opts.output == outputJSON {
		return emit(updated)
	}
	return ok("Renamed %s → %s in %s", oldName, newName, repo.Prefix)
}

func docExportCmd() *cobra.Command {
	var (
		toPath     bool
		explicitTo string
		rawInput   string
	)
	cmd := &cobra.Command{
		Use:   "export [filename]",
		Short: "Write a document's content to disk (local mode only)",
		Long: `Write a document's content to disk.

Pass --to-path to use the source path the doc was imported from
(via --from-path on add/upsert), or --to <path> to write to an
explicit path. Parent directories are created as needed and an
existing file at the destination is overwritten.

Local-only: the API can't write to the client's filesystem. In remote
mode, use ` + "`bacio doc download`" + ` and pipe to disk yourself.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput, "to", "to-path")
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocExportInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				if in.ToPath && in.To != "" {
					return fmt.Errorf("provide to OR to_path, not both")
				}
				if !in.ToPath && in.To == "" {
					return fmt.Errorf("provide to (path) or to_path=true (use stored source_path)")
				}
				if inRemoteMode() {
					return fmt.Errorf("bacio doc export is not supported in remote mode (writes to the client's filesystem); use `bacio doc download` and pipe to disk yourself")
				}
				return exportDocument(in.Filename, in.To, in.ToPath)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			if toPath && explicitTo != "" {
				return fmt.Errorf("--to-path and --to are mutually exclusive")
			}
			if !toPath && explicitTo == "" {
				return fmt.Errorf("provide --to-path (use stored source_path) or --to <path>")
			}
			if inRemoteMode() {
				return fmt.Errorf("bacio doc export is not supported in remote mode (writes to the client's filesystem); use `bacio doc download` and pipe to disk yourself")
			}
			return exportDocument(args[0], explicitTo, toPath)
		},
	}
	cmd.Flags().BoolVar(&toPath, "to-path", false, "write to the doc's stored source_path (the path used with --from-path)")
	cmd.Flags().StringVar(&explicitTo, "to", "", "write to this repo-relative path")
	addInputFlag(cmd, &rawInput)
	return cmd
}

func exportDocument(filename, explicitTo string, toPath bool) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	d, err := c.GetDocumentRaw(context.Background(), repo, filename)
	if err != nil {
		return err
	}
	var dest string
	if toPath {
		if d.SourcePath == "" {
			return fmt.Errorf("document %s has no source_path; pass an explicit destination or re-import via `bacio doc upsert --from-path`", d.Filename)
		}
		dest = d.SourcePath
	} else {
		dest = explicitTo
	}
	if err := validateRelativePath(dest); err != nil {
		return err
	}
	repoRoot := repo.Path
	if repoRoot == "" {
		return fmt.Errorf("repo path is unset; cannot resolve export destination")
	}
	absDest := filepath.Join(repoRoot, filepath.FromSlash(dest))
	if opts.dryRun {
		return emitDryRun(&docExportPreview{
			Filename:    d.Filename,
			Destination: absDest,
			Bytes:       len(d.Content),
		})
	}
	if err := os.MkdirAll(filepath.Dir(absDest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(absDest, []byte(d.Content), 0o644); err != nil {
		return err
	}
	return ok("wrote %s (%d bytes) to %s", d.Filename, len(d.Content), absDest)
}

// docExportPreview is the dry-run payload for `bacio doc export`. It reports
// the absolute path the body would be written to, plus the byte count, so
// an agent can see whether the destination path is what it expected
// without actually creating any files on disk.
type docExportPreview struct {
	Filename    string `json:"filename"`
	Destination string `json:"destination"`
	Bytes       int    `json:"bytes"`
}

// docDownloadCmd is the remote-friendly counterpart to `bacio doc export`.
// It fetches the document body and writes it to stdout (or `--to <path>`
// when the caller wants it on disk). Read-only — no audit row, no
// dry-run.
func docDownloadCmd() *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "download <filename>",
		Short: "Fetch a document's body (writes to stdout or --to <path>)",
		Long: `Read-only fetch of a document's body. By default writes to stdout
so the caller can pipe to disk; pass --to <path> to write directly to
a file (parent directories created as needed).

Works in both local and remote mode. Unlike ` + "`bacio doc export`" + `,
this verb does not require the doc to have a stored source_path and
makes no assumptions about the developer's working tree — it's just
"give me the bytes".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := openClient()
			if err != nil {
				return err
			}
			defer c.Close()
			repo, err := resolveRepoC(c)
			if err != nil {
				return err
			}
			body, err := c.DownloadDocument(context.Background(), repo, args[0])
			if err != nil {
				return err
			}
			if to == "" {
				_, err := os.Stdout.Write(body)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
				return err
			}
			return os.WriteFile(to, body, 0o644)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "write to this path instead of stdout")
	return cmd
}

func docRmCmd() *cobra.Command {
	var rawInput string
	cmd := &cobra.Command{
		Use:   "rm [filename]",
		Short: "Delete a document (and its links)",
		Args:  cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := parseJSONInput(cmd, args, rawInput)
			if err != nil {
				return err
			}
			if raw != nil {
				in, _, err := inputio.DecodeStrict[inputs.DocRmInput](raw)
				if err != nil {
					return err
				}
				if in.Filename == "" {
					return fmt.Errorf("filename is required")
				}
				return removeDocument(in.Filename)
			}
			if len(args) != 1 {
				return fmt.Errorf("requires <filename> positional or --json")
			}
			return removeDocument(args[0])
		},
	}
	addInputFlag(cmd, &rawInput)
	return cmd
}

func removeDocument(filename string) error {
	c, err := openClient()
	if err != nil {
		return err
	}
	defer c.Close()
	repo, err := resolveRepoC(c)
	if err != nil {
		return err
	}
	deleted, preview, err := c.DeleteDocument(context.Background(), repo, filename, opts.dryRun)
	if err != nil {
		return err
	}
	if opts.dryRun {
		return emitDryRun(&docDeletePreview{
			Document:     preview.Document,
			WouldDelete:  preview.WouldDelete,
			LinksRemoved: preview.Cascade.IssueLinks + preview.Cascade.FeatureLinks,
		})
	}
	return ok("deleted document %s", deleted.Filename)
}

// docDeletePreview is the dry-run payload for `bacio doc rm`. LinksRemoved
// counts how many document_links rows would cascade-delete alongside the
// document row itself.
type docDeletePreview struct {
	Document     *model.Document `json:"document"`
	WouldDelete  bool            `json:"would_delete"`
	LinksRemoved int             `json:"links_removed"`
}
