package main

import (
	"flag"
	"io"
	"os"

	"k8s.io/klog/v2"

	"github.com/nexascheduler/nexa/pkg/compliance"
)

func main() {
	var (
		inputFile string
		org       string
		from      string
		to        string
		standard  string
		format    string
	)

	klog.InitFlags(nil)
	flag.StringVar(&inputFile, "input", "", "path to audit log file (reads stdin if empty)")
	flag.StringVar(&org, "org", "", "filter by organization (empty = all orgs)")
	flag.StringVar(&from, "from", "", "start time (RFC3339, e.g. 2026-01-01T00:00:00Z)")
	flag.StringVar(&to, "to", "", "end time (RFC3339, e.g. 2026-02-01T00:00:00Z)")
	flag.StringVar(&standard, "standard", "hipaa", "compliance standard: hipaa, soc2, gdpr")
	flag.StringVar(&format, "format", "json", "output format: json, markdown")
	flag.Parse()

	var r io.Reader
	if inputFile != "" {
		f, err := os.Open(inputFile)
		if err != nil {
			klog.ErrorS(err, "failed to open audit log file",
				"path", inputFile,
				"fix", "check that the file exists and is readable")
			os.Exit(1)
		}
		defer f.Close()
		r = f
	} else {
		r = os.Stdin
	}

	entries, warnings := compliance.ReadEntries(r)
	for _, w := range warnings {
		klog.InfoS("skipped malformed line", "line", w.Line, "error", w.Err)
	}

	report, err := compliance.GenerateReport(entries, warnings, org, standard, from, to)
	if err != nil {
		klog.ErrorS(err, "failed to generate report",
			"fix", "use --standard with one of: hipaa, soc2, gdpr")
		os.Exit(1)
	}

	switch format {
	case "json":
		if err := compliance.FormatJSON(os.Stdout, report); err != nil {
			klog.ErrorS(err, "failed to write report")
			os.Exit(1)
		}
	case "markdown":
		if err := compliance.FormatMarkdown(os.Stdout, report); err != nil {
			klog.ErrorS(err, "failed to write report")
			os.Exit(1)
		}
	default:
		klog.ErrorS(nil, "unknown output format",
			"format", format,
			"fix", "use --format json or --format markdown")
		os.Exit(1)
	}
}
