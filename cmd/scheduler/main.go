// Package main is the entrypoint for the Nexa out-of-tree scheduler.
package main

import (
	"os"

	"k8s.io/component-base/cli"
	"k8s.io/kubernetes/cmd/kube-scheduler/app"

	"github.com/nexascheduler/nexa/pkg/plugins/audit"
	"github.com/nexascheduler/nexa/pkg/plugins/privacy"
	"github.com/nexascheduler/nexa/pkg/plugins/region"
)

func main() {
	command := app.NewSchedulerCommand(
		app.WithPlugin(region.Name, region.New),
		app.WithPlugin(privacy.Name, privacy.New),
		app.WithPlugin(audit.Name, audit.New),
	)
	code := cli.Run(command)
	os.Exit(code)
}
