package subcmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/segmentio/topicctl/pkg/acl"
	"github.com/segmentio/topicctl/pkg/admin"
	"github.com/segmentio/topicctl/pkg/cli"
	"github.com/segmentio/topicctl/pkg/config"
	"github.com/segmentio/topicctl/pkg/quota"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var createCmd = &cobra.Command{
	Use:               "create [resource type]",
	Short:             "creates one or more resources",
	PersistentPreRunE: createPreRun,
}

type createCmdConfig struct {
	dryRun      bool
	pathPrefix  string
	skipConfirm bool
	delete      bool

	shared sharedOptions
}

var createConfig createCmdConfig

func init() {
	createCmd.PersistentFlags().BoolVar(
		&createConfig.dryRun,
		"dry-run",
		false,
		"Do a dry-run",
	)
	createCmd.PersistentFlags().StringVar(
		&createConfig.pathPrefix,
		"path-prefix",
		os.Getenv("TOPICCTL_ACL_PATH_PREFIX"),
		"Prefix for ACL config paths",
	)
	createCmd.PersistentFlags().BoolVar(
		&createConfig.skipConfirm,
		"skip-confirm",
		false,
		"Skip confirmation prompts during creation process",
	)
	createCmd.PersistentFlags().BoolVar(
		&createConfig.delete,
		"delete",
		false,
		"Delete resources which are not provided in the list of resources ('sync' behavior; requires a single resources specfile)",
	)

	addSharedFlags(createCmd, &createConfig.shared)
	createCmd.AddCommand(
		createACLsCmd(),
		createQuotasCmd(),
	)
	RootCmd.AddCommand(createCmd)
}

func createPreRun(cmd *cobra.Command, args []string) error {
	if err := RootCmd.PersistentPreRunE(cmd, args); err != nil {
		return err
	}
	return createConfig.shared.validate()
}

func createACLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "acls [acl configs]",
		Short:   "creates ACLs from configuration files",
		Args:    cobra.MinimumNArgs(1),
		RunE:    createACLRun,
		PreRunE: createPreRun,
	}

	return cmd
}

func createQuotasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "quotas [quotas configs]",
		Short:   "creates quotas from configuration files",
		Args:    cobra.MinimumNArgs(1),
		RunE:    createQuotaRun,
		PreRunE: createPreRun,
	}

	return cmd
}

func createACLRun(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Keep a cache of the admin clients with the cluster config path as the key
	adminClients := map[string]admin.Client{}

	defer func() {
		for _, adminClient := range adminClients {
			adminClient.Close()
		}
	}()

	matchCount := 0

	if len(args) > 1 && createConfig.delete {
		return fmt.Errorf("When --delete option is given, only 1 ACLs configuration is allowed")
	}

	for _, arg := range args {
		if createConfig.pathPrefix != "" && !filepath.IsAbs(arg) {
			arg = filepath.Join(createConfig.pathPrefix, arg)
		}

		matches, err := filepath.Glob(arg)
		if err != nil {
			return err
		}

		for _, match := range matches {
			matchCount++
			if err := createACL(ctx, match, adminClients); err != nil {
				return err
			}
		}
	}

	if matchCount == 0 {
		return fmt.Errorf("No ACL configs match the provided args (%+v)", args)
	}

	return nil
}

func createACL(
	ctx context.Context,
	aclConfigPath string,
	adminClients map[string]admin.Client,
) error {
	clusterConfigPath, err := clusterConfigForCreate(aclConfigPath)
	if err != nil {
		return err
	}

	aclConfigs, err := config.LoadACLsFile(aclConfigPath)
	if err != nil {
		return err
	}

	clusterConfig, err := config.LoadClusterFile(clusterConfigPath, createConfig.shared.expandEnv)
	if err != nil {
		return err
	}

	adminClient, ok := adminClients[clusterConfigPath]
	if !ok {
		adminClient, err = clusterConfig.NewAdminClient(
			ctx,
			nil,
			config.AdminClientOpts{
				ReadOnly:                  createConfig.dryRun,
				UsernameOverride:          createConfig.shared.saslUsername,
				PasswordOverride:          createConfig.shared.saslPassword,
				SecretsManagerArnOverride: createConfig.shared.saslSecretsManagerArn,
			},
		)
		if err != nil {
			return err
		}
		adminClients[clusterConfigPath] = adminClient
	}

	cliRunner := cli.NewCLIRunner(adminClient, log.Infof, false)

	for _, aclConfig := range aclConfigs {
		aclConfig.SetDefaults()
		log.Infof(
			"Processing ACL %s in config %s with cluster config %s",
			aclConfig.Meta.Name,
			aclConfigPath,
			clusterConfigPath,
		)

		aclAdminConfig := acl.ACLAdminConfig{
			DryRun:        createConfig.dryRun,
			SkipConfirm:   createConfig.skipConfirm,
			Delete:        createConfig.delete,
			ACLConfig:     aclConfig,
			ClusterConfig: clusterConfig,
		}

		if err := cliRunner.CreateACL(ctx, aclAdminConfig); err != nil {
			return err
		}
	}

	return nil
}

func createQuotaRun(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	// Keep a cache of the admin clients with the cluster config path as the key
	adminClients := map[string]admin.Client{}

	defer func() {
		for _, adminClient := range adminClients {
			adminClient.Close()
		}
	}()

	matchCount := 0

	if len(args) > 1 && createConfig.delete {
		return fmt.Errorf("When --delete option is given, only 1 quotas configuration is allowed")
	}

	for _, arg := range args {
		if createConfig.pathPrefix != "" && !filepath.IsAbs(arg) {
			arg = filepath.Join(createConfig.pathPrefix, arg)
		}

		matches, err := filepath.Glob(arg)
		if err != nil {
			return err
		}

		for _, match := range matches {
			matchCount++
			if err := createQuota(ctx, match, adminClients); err != nil {
				return err
			}
		}
	}

	if matchCount == 0 {
		return fmt.Errorf("No quota configs match the provided args (%+v)", args)
	}

	return nil
}

func createQuota(
	ctx context.Context,
	quotaConfigPath string,
	adminClients map[string]admin.Client,
) error {
	clusterConfigPath, err := clusterConfigForCreate(quotaConfigPath)
	if err != nil {
		return err
	}

	quotaConfigs, err := config.LoadQuotasFile(quotaConfigPath)
	if err != nil {
		return err
	}

	clusterConfig, err := config.LoadClusterFile(clusterConfigPath, createConfig.shared.expandEnv)
	if err != nil {
		return err
	}

	adminClient, ok := adminClients[clusterConfigPath]
	if !ok {
		adminClient, err = clusterConfig.NewAdminClient(
			ctx,
			nil,
			config.AdminClientOpts{
				ReadOnly:                  createConfig.dryRun,
				UsernameOverride:          createConfig.shared.saslUsername,
				PasswordOverride:          createConfig.shared.saslPassword,
				SecretsManagerArnOverride: createConfig.shared.saslSecretsManagerArn,
			},
		)
		if err != nil {
			return err
		}
		adminClients[clusterConfigPath] = adminClient
	}

	cliRunner := cli.NewCLIRunner(adminClient, log.Infof, false)

	for _, quotaConfig := range quotaConfigs {
		log.Infof(
			"Processing quotas %s in config %s with cluster config %s",
			quotaConfig.Meta.Name,
			quotaConfigPath,
			clusterConfigPath,
		)

		quotaAdminConfig := quota.QuotaAdminConfig{
			DryRun:        createConfig.dryRun,
			SkipConfirm:   createConfig.skipConfirm,
			Delete:        createConfig.delete,
			QuotaConfig:   quotaConfig,
			ClusterConfig: clusterConfig,
		}

		if err := cliRunner.CreateQuota(ctx, quotaAdminConfig); err != nil {
			return err
		}
	}

	return nil
}

func clusterConfigForCreate(aclConfigPath string) (string, error) {
	if createConfig.shared.clusterConfig != "" {
		return createConfig.shared.clusterConfig, nil
	}

	return filepath.Abs(
		filepath.Join(
			filepath.Dir(aclConfigPath),
			"..",
			"cluster.yaml",
		),
	)
}
