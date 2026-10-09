package custom

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"text/tabwriter"
	"unicode"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

func registerCostReportCommands(root *cli.Command) {
	root.Commands = append(root.Commands, &cli.Command{
		Name: "costs", Usage: "Report organization costs by project.", HideHelpCommand: true,
		Commands: []*cli.Command{{
			Name: "report", Usage: "Total project costs over an explicit date range.", HideHelpCommand: true,
			DisableSliceFlagSeparator: true,
			Metadata: map[string]any{
				"completion-root-flag-values": map[string][]string{"format": {"auto", "text", "json"}},
				"help-content": clihelp.Content{
					Description: "Requires OPENAI_ADMIN_KEY. Start is inclusive; end is exclusive. " +
						"Reads every page before printing a report. Supports --format text or json, or --export csv. " +
						"Does not read stdin or support --transform or --raw-output.",
					Examples: []clihelp.Example{{Description: "Report one week of project costs:", Command: "costs report --from 2026-10-01 --to 2026-10-08"}},
				},
			},
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "from", Usage: "Inclusive start date, YYYY-MM-DD", Required: true, OnlyOnce: true},
				&cli.StringFlag{Name: "to", Usage: "Exclusive end date, YYYY-MM-DD", Required: true, OnlyOnce: true},
				&cli.StringFlag{Name: "timezone", Usage: "UTC or IANA timezone for both local midnight boundaries", Value: "UTC", OnlyOnce: true},
				&cli.StringFlag{Name: "group-by", Usage: "Report grouping: project", Value: "project", OnlyOnce: true},
				&cli.StringSliceFlag{Name: "project-id", Usage: "Include only this exact project ID; repeat for multiple projects"},
				&cli.StringFlag{Name: "export", Usage: "Write CSV to stdout: csv; use without --format", OnlyOnce: true},
			},
			Action: handleCostReport,
		}},
	})
}

type projectCostReport struct {
	costReportRange
	GroupBy string                        `json:"group_by"`
	Rows    []transformers.ProjectCostRow `json:"rows"`
}

func handleCostReport(parent context.Context, command *cli.Command) (err error) {
	root := command.Root()
	if command.Args().Present() {
		return &localUtilityError{message: "cost reports take no positional arguments"}
	}
	format := strings.ToLower(root.String("format"))
	if format == "" || format == "auto" {
		format = "text"
	}
	if root.IsSet("transform") || root.IsSet("raw-output") {
		return &localUtilityError{message: "cost reports do not support --transform or --raw-output; use --format json"}
	}
	if format != "text" && format != "json" {
		return &localUtilityError{message: "cost reports support --format auto, text, or json; use --export csv for CSV"}
	}
	if command.IsSet("export") {
		if command.String("export") != "csv" || root.IsSet("format") {
			return &localUtilityError{message: "use --export csv without --format"}
		}
		format = "csv"
	}
	if command.String("group-by") != "project" {
		return &localUtilityError{message: "cost reports support --group-by project"}
	}
	for _, project := range command.StringSlice("project-id") {
		if project == "" {
			return &localUtilityError{message: "--project-id requires a nonempty project ID"}
		}
	}
	period, err := parseCostReportRange(command.String("from"), command.String("to"), command.String("timezone"))
	if err != nil {
		return &localUtilityError{message: err.Error(), cause: err}
	}
	options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	interruptsDone := make(chan struct{})
	go func() {
		defer close(interruptsDone)
		for range interrupts {
			cancel()
		}
	}()
	stop := sync.OnceFunc(func() {
		stopCostReportInterrupts(interrupts, interruptsDone)
	})
	defer func() {
		stop()
		interrupted := ctx.Err() != nil && parent.Err() == nil
		if interrupted && (err == nil || errors.Is(err, context.Canceled)) {
			if err == nil {
				err = ctx.Err()
			}
			err = &costReportInterrupt{err}
		}
	}()
	client := openai.NewClient(GetDefaultRequestOptions(command)...)
	params := openai.AdminOrganizationUsageCostsParams{
		StartTime: period.StartTime, EndTime: openai.Int(period.EndTime), Limit: openai.Int(180),
		BucketWidth: openai.AdminOrganizationUsageCostsParamsBucketWidth1d,
		GroupBy:     []string{"project_id"}, ProjectIDs: command.StringSlice("project-id"),
	}
	accumulator := transformers.NewProjectCostAccumulator()
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var raw []byte
		requestOptions := append([]option.RequestOption{}, options...)
		requestOptions = append(requestOptions, option.WithResponseBodyInto(&raw))
		if _, err := client.Admin.Organization.Usage.Costs(ctx, params, requestOptions...); err != nil {
			return &costReportRequestError{err}
		}
		more, cursor, err := accumulator.AddPage(ctx, raw)
		if err != nil {
			if errors.Is(err, transformers.ErrProjectCostExpansion) {
				return &localUtilityError{
					message: "Exact aggregation needs too many additional decimal digits. " +
						"Use admin organization usage costs for raw records. No report was written.",
					cause: err,
				}
			}
			return &localUtilityError{message: "The Costs API returned incomplete or invalid report data. No report was written.", cause: err}
		}
		if !more {
			break
		}
		if cursor == "" || seen[cursor] {
			return &localUtilityError{message: "cost report pagination stalled: missing or repeated next_page; no report was written"}
		}
		seen[cursor] = true
		params.Page = openai.String(cursor)
	}
	rows, err := accumulator.Rows(ctx)
	if err != nil {
		return &localUtilityError{message: "Could not total the Costs API amounts exactly. No report was written.", cause: err}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Restore ordinary interrupt handling before a potentially blocked stdout write.
	// The caller's context still controls output; Ctrl+C can terminate a blocked pipe.
	stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	report := projectCostReport{costReportRange: period, GroupBy: "project", Rows: rows}
	if err := writeCostReport(parent, root.Writer, format, report); err != nil {
		return &localUtilityError{message: "Could not write the cost report. Output may be incomplete; check the output file or pipe.", cause: err}
	}
	return parent.Err()
}

func stopCostReportInterrupts(interrupts chan os.Signal, handled <-chan struct{}) {
	// Stop guarantees no later sends. Drain delivered signals before proceeding.
	signal.Stop(interrupts)
	close(interrupts)
	<-handled
}

type costReportRequestError struct{ error }

func (e *costReportRequestError) Unwrap() error { return e.error }

// Keep API error identity and structured payloads while explaining this Admin workflow.
func costReportAPIErrorMessage(failure error, apierr *openai.Error) string {
	var reportFailure *costReportRequestError
	if !errors.As(failure, &reportFailure) {
		return ""
	}
	switch apierr.StatusCode {
	case http.StatusUnauthorized:
		return "Cost reports require an organization Admin API key.\nSet OPENAI_ADMIN_KEY; a project API key cannot replace it."
	case http.StatusForbidden:
		return "Check your Admin API key's access to organization costs.\nConfirm OPENAI_ADMIN_KEY and the selected organization."
	default:
		return ""
	}
}

type costReportInterrupt struct{ error }

func (e *costReportInterrupt) Unwrap() error { return e.error }
func (e *costReportInterrupt) ExitCode() int { return 130 }

func writeCostReport(ctx context.Context, destination io.Writer, format string, report projectCostReport) error {
	out := outputWriter{ctx: ctx, out: destination}
	switch format {
	case "json":
		return json.NewEncoder(out).Encode(report)
	case "csv":
		terminalCSV := isTerminal(destination)
		csvOut := csv.NewWriter(out)
		if err := csvOut.Write([]string{"project_id", "currency", "amount"}); err != nil {
			return err
		}
		for _, row := range report.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			project := ""
			if row.ProjectID != nil {
				project = *row.ProjectID
			}
			project = costReportCSVText(project)
			currency := costReportCSVText(row.Currency)
			if terminalCSV {
				project = costReportCell(project)
				currency = costReportCell(currency)
			}
			if err := csvOut.Write([]string{project, currency, row.Amount}); err != nil {
				return err
			}
		}
		csvOut.Flush()
		return csvOut.Error()
	default:
		if _, err := fmt.Fprintf(out, "Period: %s to %s %s (end exclusive)\n", report.From, report.To, report.Timezone); err != nil {
			return err
		}
		if len(report.Rows) == 0 {
			_, err := io.WriteString(out, "No cost records.\n")
			return err
		}
		table := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		if _, err := fmt.Fprintln(table, "PROJECT\tCURRENCY\tAMOUNT"); err != nil {
			return err
		}
		for _, row := range report.Rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			project := "(unattributed)"
			if row.ProjectID != nil {
				project = *row.ProjectID
				if project == "" {
					project = "(empty project ID)"
				}
			}
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\n", costReportCell(project), costReportCell(row.Currency), row.Amount); err != nil {
				return err
			}
		}
		return table.Flush()
	}
}

func costReportCell(value string) string {
	return readable.Text(jsonview.SanitizeTerminalString(value))
}

// Protect text cells when spreadsheets interpret CSV. JSON retains exact IDs.
func costReportCSVText(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if strings.ContainsAny(value, "\t\r\n") || trimmed != "" && strings.ContainsRune("=+-@'", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
