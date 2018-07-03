// Package runtime handles constructing an execution graph for each action
// based on configuration and defaults. The handlers can then execute this
// graph.
package runtime

import (
	log "gopkg.in/inconshreveable/log15.v2"

	"github.com/hashicorp/go-version"
)

type TerraformExec interface {
	RunCommandWithVersion(log log.Logger, path string, args []string, v *version.Version, workspace string) (string, error)
}

// MustConstraint returns a constraint. It panics on error.
func MustConstraint(constraint string) version.Constraints {
	c, err := version.NewConstraint(constraint)
	if err != nil {
		panic(err)
	}
	return c
}
