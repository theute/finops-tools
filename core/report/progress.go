package report

import "github.com/openshift-online/finops-tools/core/progress"

// Progress reports long-running steps while building a report.
type Progress = progress.Reporter

// noopProgress discards progress messages.
type noopProgress struct{}

func (noopProgress) Step(string) {}
