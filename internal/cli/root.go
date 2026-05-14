package cli

import (
	"github.com/spf13/cobra"

	"github.com/mrgeoffrich/bacio/internal/version"
)

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
)

type globalOpts struct {
	output     outputFormat
	dbPath     string
	user       string
	dryRun     bool
	remote     string
	token      string
	cpuProfile string
	memProfile string
	traceFile  string
}

var opts = globalOpts{output: outputText}

// NewRoot builds the cobra command tree and returns it alongside a
// cleanup func. main.go must call the cleanup func after Execute()
// returns (on success and error alike) so CPU/heap profiles flush even
// when a command exits via an error path.
func NewRoot() (*cobra.Command, func()) {
	root := &cobra.Command{
		Use:           "bacio",
		Short:         "bacio: a local-first issue tracker, CLI-first",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := validateActorFlag(); err != nil {
				return err
			}
			return startProfiling()
		},
	}
	root.PersistentFlags().VarP(newOutputFlag(&opts.output), "output", "o", "output format: text|json")
	root.PersistentFlags().StringVar(&opts.dbPath, "db", "", "override database path (default: ~/.bacio/db.sqlite)")
	root.PersistentFlags().StringVar(&opts.user, "user", "", "actor name recorded in history (defaults to OS user; AI agents must pass this explicitly)")
	root.PersistentFlags().BoolVar(&opts.dryRun, "dry-run", false, "validate the request and emit the projected result without writing to the database (no audit log entry)")
	root.PersistentFlags().StringVar(&opts.remote, "remote", "", "talk to a bacio api server at this URL instead of the local DB; falls back to BACIO_REMOTE")
	root.PersistentFlags().StringVar(&opts.token, "token", "", "bearer token for the remote API; falls back to BACIO_API_TOKEN")
	root.PersistentFlags().StringVar(&opts.cpuProfile, "cpuprofile", "", "write a CPU profile to this path (dev/debug)")
	root.PersistentFlags().StringVar(&opts.memProfile, "memprofile", "", "write a heap profile to this path (dev/debug)")
	root.PersistentFlags().StringVar(&opts.traceFile, "trace", "", "write an execution trace to this path (dev/debug)")
	_ = root.PersistentFlags().MarkHidden("cpuprofile")
	_ = root.PersistentFlags().MarkHidden("memprofile")
	_ = root.PersistentFlags().MarkHidden("trace")

	root.AddCommand(
		newInitCmd(),
		newRepoCmd(),
		newFeatureCmd(),
		newIssueCmd(),
		newCommentCmd(),
		newLinkCmd(),
		newUnlinkCmd(),
		newPRCmd(),
		newTagCmd(),
		newDocCmd(),
		newStatusCmd(),
		newHistoryCmd(),
		newSchemaCmd(),
		newAPICmd(),
		newSyncCmd(),
		newInstallSkillCmd(),
		newInstallSampleSkillsCmd(),
		newTUICmd(),
		newDemoCmd(),
		newAgentCmd(),
	)
	return root, stopProfiling
}
