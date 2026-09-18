package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/lunemis/mux/tmux"
	"github.com/lunemis/mux/ui"
)

var version = "dev"

// quickCycleFlag enables OQ8 hold-and-cycle mode (Option+Tab popup).
// docs/changelog/2026-09-16-b-option-tab-quick-switch.md
var quickCycleFlag bool

// reverseStartFlag opens the quick popup on the LAST row (OQ11): the opening
// tap is consumed by the tmux display-popup bind, so the reverse direction
// must be passed as a flag — start-on-last = the opening tap counts as the
// first reverse step (wrap from row 1).
// docs/changelog/2026-09-17-a-option-tab-reverse-cycle.md
var reverseStartFlag bool

func main() {
	rootCmd := &cobra.Command{
		Use:     "mux",
		Short:   "TUI tmux session manager",
		Version: version,
		RunE:    runTUI,
		// Suppress cobra's default completion and help subcommands
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	rootCmd.SetVersionTemplate("mux {{.Version}}\n")

	popupCmd := &cobra.Command{
		Use:   "popup",
		Short: "Open mux as a tmux popup overlay",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tmux.OpenPopup()
		},
	}

	setupKeybindCmd := &cobra.Command{
		Use:   "setup-keybind [key]",
		Short: fmt.Sprintf("Add popup keybinding to tmux config (default: %s)", tmux.DefaultBindKey),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := tmux.DefaultBindKey
			if len(args) > 0 {
				key = args[0]
			}
			return tmux.SetupKeybind(key)
		},
	}

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show AI session summary for tmux statusbar",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}

	rootCmd.AddCommand(popupCmd, setupKeybindCmd, statusCmd)
	rootCmd.Flags().BoolVar(&quickCycleFlag, "quick-cycle", false,
		"quick-cycle mode: start on row 2, ESC+Tab cycles, SIGUSR1 attaches (OQ8)")
	rootCmd.Flags().BoolVar(&reverseStartFlag, "reverse-start", false,
		"with --quick-cycle: open on the last row so the opening tap counts as the first reverse step (OQ11)")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runStatus() error {
	sessions, err := tmux.ListSessions()
	if err != nil {
		return err
	}

	var parts []string
	for _, s := range sessions {
		tool, ok := tmux.LookupAITool(s.ActiveCommand)
		if !ok {
			continue
		}
		parts = append(parts, tool.Icon)
	}

	if len(parts) == 0 {
		return nil // no AI sessions, output nothing
	}

	fmt.Print(fmt.Sprintf(" %s ", joinWith(parts, " ")))
	return nil
}

func joinWith(parts []string, sep string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}

func runTUI(cmd *cobra.Command, args []string) error {
	p := tea.NewProgram(ui.NewModelQuickCycleStart(quickCycleFlag, reverseStartFlag), tea.WithAltScreen())

	// OQ8: Karabiner sends SIGUSR1 (pkill -USR1 -x terminal-switcher) when
	// the user releases the left Option key. The handler is installed for
	// every mode; the model ignores the commit outside quick-cycle.
	usrCh := make(chan os.Signal, 1)
	signal.Notify(usrCh, syscall.SIGUSR1)
	go func() {
		for range usrCh {
			p.Send(ui.CycleCommitMsg{})
		}
	}()

	result, err := p.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	if m, ok := result.(ui.Model); ok {
		if name := m.AttachName(); name != "" {
			if err := ui.AttachToSession(name, m.AttachWindowIndex(), m.AttachPaneIndex()); err != nil {
				return fmt.Errorf("failed to attach: %w", err)
			}
		}
	}
	return nil
}
