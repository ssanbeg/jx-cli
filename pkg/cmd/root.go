package cmd

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	v1 "github.com/jenkins-x/jx-api/v4/pkg/apis/jenkins.io/v1"
	"github.com/jenkins-x/jx-api/v4/pkg/client/clientset/versioned"
	"github.com/jenkins-x/jx-cli/pkg/cmd/dashboard"
	"github.com/jenkins-x/jx-cli/pkg/cmd/namespace"
	"github.com/jenkins-x/jx-cli/pkg/cmd/ui"
	"github.com/jenkins-x/jx-cli/pkg/cmd/upgrade"
	"github.com/jenkins-x/jx-cli/pkg/cmd/version"
	"github.com/jenkins-x/jx-cli/pkg/plugins"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/v3/pkg/extensions"
	"github.com/jenkins-x/jx-helpers/v3/pkg/homedir"
	"github.com/jenkins-x/jx-helpers/v3/pkg/httphelpers"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"github.com/jenkins-x/jx-logging/v3/pkg/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/util/json"
)

// Main creates the new command
func Main(args []string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jx",
		Short: "Jenkins X 3.x alpha command line",
		Run:   runHelp,
	}

	po := &templates.Options{}
	getPluginCommandGroups := func() (templates.PluginCommandGroups, bool) {
		verifier := &extensions.CommandOverrideVerifier{
			Root:        cmd,
			SeenPlugins: make(map[string]string),
		}
		pluginCommandGroups, err := po.GetPluginCommandGroups(verifier, plugins.Plugins)
		if err != nil {
			log.Logger().Errorf("%v", err)
		}
		return pluginCommandGroups, po.ManagedPluginsEnabled
	}
	doCmd := func(cmd *cobra.Command, args []string) {
		handleCommand(po, cmd, args, getPluginCommandGroups)
	}

	generalCommands := []*cobra.Command{
		cobras.SplitCommand(dashboard.NewCmdDashboard()),
		cobras.SplitCommand(namespace.NewCmdNamespace()),
		cobras.SplitCommand(ui.NewCmdUI()),
		cobras.SplitCommand(upgrade.NewCmdUpgrade()),
		cobras.SplitCommand(version.NewCmdVersion()),
	}

	// aliases to classic jx commands...
	getCmd := &cobra.Command{
		Use:   "get TYPE [flags]",
		Short: "Display one or more resources",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
		SuggestFor: []string{"list", "ps"},
	}
	addCmd := &cobra.Command{
		Use:   "add TYPE [flags]",
		Short: "Adds one or more resources",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
	}
	getBuildCmd := &cobra.Command{
		Use:     "build TYPE [flags]",
		Short:   "Display one or more resources relating to a pipeline build",
		Aliases: []string{"builds"},
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
	}
	createCmd := &cobra.Command{
		Use:   "create TYPE [flags]",
		Short: "Create one or more resources",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
		SuggestFor: []string{"new", "make"},
	}
	startCmd := &cobra.Command{
		Use:   "start TYPE [flags]",
		Short: "Starts a resource",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
	}
	stopCmd := &cobra.Command{
		Use:   "stop TYPE [flags]",
		Short: "Stops a resource",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			helper.CheckErr(err)
		},
	}
	addCmd.AddCommand(
		aliasCommand(cmd, doCmd, "app", []string{"gitops", "helmfile", "add"}, "chart"),
	)
	getCmd.AddCommand(
		getBuildCmd,
		aliasCommand(cmd, doCmd, "activities", []string{"pipeline", "activities"}, "act", "activity"),
		aliasCommand(cmd, doCmd, "application", []string{"application", "get"}, "app", "apps", "applications"),
		aliasCommand(cmd, doCmd, "pipelines", []string{"pipeline", "get"}, "pipeline"),
		aliasCommand(cmd, doCmd, "previews", []string{"preview", "get"}, "preview"),
	)
	getBuildCmd.AddCommand(
		aliasCommand(cmd, doCmd, "logs", []string{"pipeline", "logs"}, "log"),
		aliasCommand(cmd, doCmd, "pods", []string{"pipeline", "pods"}, "pod"),
	)
	createCmd.AddCommand(
		aliasCommand(cmd, doCmd, "quickstart", []string{"project", "quickstart"}, "qs"),
		aliasCommand(cmd, doCmd, "spring", []string{"project", "spring"}, "sb"),
		aliasCommand(cmd, doCmd, "project", []string{"project"}),
		aliasCommand(cmd, doCmd, "pullrequest", []string{"project", "pullrequest"}, "pr"),
	)
	startCmd.AddCommand(
		aliasCommand(cmd, doCmd, "pipeline", []string{"pipeline", "start"}, "pipelines"),
	)
	stopCmd.AddCommand(
		aliasCommand(cmd, doCmd, "pipeline", []string{"pipeline", "stop"}, "pipelines"),
	)
	generalCommands = append(generalCommands, addCmd, getCmd, createCmd, startCmd, stopCmd,
		aliasCommand(cmd, doCmd, "import", []string{"project", "import"}, "log"),
	)

	cmd.AddCommand(generalCommands...)
	groups := templates.CommandGroups{
		{
			Message:  "General:",
			Commands: generalCommands,
		},
	}
	groups.Add(cmd)
	filters := []string{"options"}

	templates.ActsAsRootCommand(cmd, filters, getPluginCommandGroups, groups...)
	handleCommand(po, cmd, args, getPluginCommandGroups)
	return cmd
}

func handleCommand(po *templates.Options, cmd *cobra.Command, args []string, getPluginCommandGroups func() (templates.PluginCommandGroups, bool)) {
	managedPlugins := &managedPluginHandler{
		JXClient:  po.JXClient,
		Namespace: po.Namespace,
	}
	localPlugins := &localPluginHandler{}

	if len(args) == 0 {
		args = os.Args
	}
	if len(args) > 1 {
		cmdPathPieces := args[1:]

		pluginDir, err := homedir.DefaultPluginBinDir()
		if err != nil {
			log.Logger().Errorf("%v", err)
			os.Exit(1)
		}

		// only look for suitable executables if
		// the specified command does not already exist
		if _, _, err := cmd.Find(cmdPathPieces); err != nil {
			if _, managedPluginsEnabled := getPluginCommandGroups(); managedPluginsEnabled {
				if err := handleEndpointExtensions(managedPlugins, cmdPathPieces, pluginDir); err != nil {
					log.Logger().Errorf("%v", err)
					os.Exit(1)
				}
			} else {
				if err := handleEndpointExtensions(localPlugins, cmdPathPieces, pluginDir); err != nil {
					log.Logger().Errorf("%v", err)
					os.Exit(1)
				}
			}
		}
	}
}

func aliasCommand(rootCmd *cobra.Command, fn func(cmd *cobra.Command, args []string), name string, args []string, aliases ...string) *cobra.Command {
	realArgs := append([]string{"jx"}, args...)
	cmd := &cobra.Command{
		Use:     name,
		Short:   "alias for: jx " + name,
		Aliases: aliases,
		Run: func(cmd *cobra.Command, args []string) {
			realArgs = append(realArgs, args...)
			log.Logger().Debugf("about to invoke alias: %s", strings.Join(realArgs, " "))
			fn(rootCmd, realArgs)
		},
		SuggestFor:         []string{"jx " + name},
		DisableFlagParsing: true,
	}
	return cmd
}

func runHelp(cmd *cobra.Command, args []string) {
	cmd.Help() //nolint:errcheck
}

// PluginHandler is capable of parsing command line arguments
// and performing executable filename lookups to search
// for valid plugin files, and execute found plugins.
type PluginHandler interface {
	// Lookup receives a potential filename and returns
	// a full or relative path to an executable, if one
	// exists at the given filename, or an error.
	Lookup(filename string, pluginBinDir string) (string, error)
	// Execute receives an executable's filepath, a slice
	// of arguments, and a slice of environment variables
	// to relay to the executable.
	Execute(executablePath string, cmdArgs, environment []string) error
}

type managedPluginHandler struct {
	JXClient  versioned.Interface
	Namespace string
	localPluginHandler
}

// Lookup implements PluginHandler
func (h *managedPluginHandler) Lookup(filename, pluginBinDir string) (string, error) {
	return h.localPluginHandler.Lookup(filename, pluginBinDir)
}

func findStandardPlugin(name string) (*v1.Plugin, error) {
	u := "https://api.github.com/repos/jenkins-x-plugins/" + name + "/releases/latest"

	client := httphelpers.GetClient()
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create http request for %s", u)
	}
	resp, err := client.Do(req)
	if err != nil {
		if resp != nil {
			return nil, errors.Wrapf(err, "failed to GET endpoint %s with status %s", u, resp.Status)
		}
		return nil, errors.Wrapf(err, "failed to GET endpoint %s", u)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read response from %s", u)
	}

	release := &githubRelease{}
	err = json.Unmarshal(body, release)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal release from %s", u)
	}
	version := strings.TrimPrefix(release.TagName, "v")
	if version == "" {
		return nil, nil
	}

	plugin := extensions.CreateJXPlugin("jenkins-x-plugins", strings.TrimPrefix(name, "jx-"), version)
	return &plugin, nil
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// Execute implements PluginHandler
func (h *managedPluginHandler) Execute(executablePath string, cmdArgs, environment []string) error {
	return h.localPluginHandler.Execute(executablePath, cmdArgs, environment)
}

type localPluginHandler struct{}

// Lookup implements PluginHandler
func (h *localPluginHandler) Lookup(filename, pluginBinDir string) (string, error) {
	path, err := exec.LookPath(filename)
	if err != nil {
		// lets see if the plugin is a standard plugin...
		plugin, err2 := findStandardPlugin(filename)
		if err2 != nil {
			return "", errors.Wrapf(err2, "failed to load plugin %s", filename)
		}
		if plugin != nil {
			return extensions.EnsurePluginInstalled(*plugin, pluginBinDir)
		}
		return "", err
	}
	return path, nil
}

// Execute implements PluginHandler
func (h *localPluginHandler) Execute(executablePath string, cmdArgs, environment []string) error {
	// Windows does not support exec syscall.
	if runtime.GOOS == "windows" {
		cmd := exec.Command(executablePath, cmdArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Env = environment
		err := cmd.Run()
		if err == nil {
			os.Exit(0)
		}
		return err
	}

	// invoke cmd binary relaying the environment and args given
	// append executablePath to cmdArgs, as execve will make first argument the "binary name".
	return syscall.Exec(executablePath, append([]string{executablePath}, cmdArgs...), environment)
}

func handleEndpointExtensions(pluginHandler PluginHandler, cmdArgs []string, pluginBinDir string) error {
	var remainingArgs []string // all "non-flag" arguments

	for idx := range cmdArgs {
		if strings.HasPrefix(cmdArgs[idx], "-") {
			break
		}
		remainingArgs = append(remainingArgs, strings.Replace(cmdArgs[idx], "-", "_", -1))
	}

	foundBinaryPath := ""

	// attempt to find binary, starting at longest possible name with given cmdArgs
	for len(remainingArgs) > 0 {
		commandName := fmt.Sprintf("jx-%s", strings.Join(remainingArgs, "-"))

		// lets try the correct plugin versions first
		path := ""
		var err error
		for i := range plugins.Plugins {
			p := plugins.Plugins[i]
			if p.Spec.Name == commandName {
				path, err = extensions.EnsurePluginInstalled(p, pluginBinDir)
				if err != nil {
					return errors.Wrapf(err, "failed to install binary plugin %s version %s to %s", commandName, p.Spec.Version, pluginBinDir)
				}
				if path != "" {
					break
				}
			}
		}

		// lets see if there's a local build of the plugin on the PATH for developers...
		localPath, err := pluginHandler.Lookup(commandName, pluginBinDir)
		if err == nil && localPath != "" {
			path = localPath
		}
		if path != "" {
			foundBinaryPath = path
			break
		}
		remainingArgs = remainingArgs[:len(remainingArgs)-1]
	}

	if foundBinaryPath == "" {
		return nil
	}

	nextArgs := cmdArgs[len(remainingArgs):]
	log.Logger().Debugf("using the plugin command: %s", termcolor.ColorInfo(foundBinaryPath+" "+strings.Join(nextArgs, " ")))

	// invoke cmd binary relaying the current environment and args given
	// remainingArgs will always have at least one element.
	// execute will make remainingArgs[0] the "binary name".
	if err := pluginHandler.Execute(foundBinaryPath, nextArgs, os.Environ()); err != nil {
		return err
	}
	return nil
}

// FindPluginBinary tries to find the jx-foo binary plugin in the plugins dir `~/.jx/plugins/jx/bin` dir `
func FindPluginBinary(pluginDir, commandName string) string {
	if pluginDir != "" {
		files, err := ioutil.ReadDir(pluginDir)
		if err != nil {
			log.Logger().Debugf("failed to read plugin dir %s", err.Error())
		} else {
			prefix := commandName + "-"
			for _, f := range files {
				name := f.Name()
				if strings.HasPrefix(name, prefix) {
					path := filepath.Join(pluginDir, name)
					log.Logger().Debugf("found plugin %s at %s", commandName, path)
					return path
				}
			}
		}
	}
	return ""
}
