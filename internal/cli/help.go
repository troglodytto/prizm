package cli

import (
	"github.com/spf13/cobra"

	"github.com/troglodytto/prizm/internal/style"
)

// Cobra renders help from a text template with no styling of its own, so a
// tool that styles its own output still has one completely plain surface —
// and it is the surface new users see first. These templates restyle it to
// match everything else: faint section labels, bold command and flag names.
const usageTemplate = `{{cliSection "usage"}}
  {{cliName .UseLine}}{{if .HasAvailableSubCommands}}
  {{cliName .CommandPath}} [command]{{end}}
{{- if gt (len .Aliases) 0}}

{{cliSection "aliases"}}
  {{.NameAndAliases}}
{{- end}}
{{- if .HasExample}}

{{cliSection "examples"}}
{{.Example}}
{{- end}}
{{- if .HasAvailableSubCommands}}

{{cliSection "commands"}}
{{- range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{cliName (rpad .Name .NamePadding)}} {{cliDetail .Short}}
{{- end}}{{end}}
{{- end}}
{{- if .HasAvailableLocalFlags}}

{{cliSection "flags"}}
{{cliFlags (trimTrailingWhitespaces .LocalFlags.FlagUsages)}}
{{- end}}
{{- if .HasAvailableInheritedFlags}}

{{cliSection "global flags"}}
{{cliFlags (trimTrailingWhitespaces .InheritedFlags.FlagUsages)}}
{{- end}}
{{- if .HasAvailableSubCommands}}

{{cliDetail (printf "%s [command] --help for more about a command" .CommandPath)}}
{{- end}}
`

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

// registerHelpStyling installs the templates and the funcs they call.
func registerHelpStyling(root *cobra.Command) {
	cobra.AddTemplateFunc("cliSection", style.Section)
	cobra.AddTemplateFunc("cliName", style.CommandName)
	cobra.AddTemplateFunc("cliDetail", style.Detail)
	cobra.AddTemplateFunc("cliFlags", style.Flags)

	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(helpTemplate)
}
