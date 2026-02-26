// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package cli provides helper functions for CLI applications using the SSA manager.
package cli

import (
	"context"

	"github.com/fluxcd/cli-utils/pkg/object"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
)

// LogApplyResults logs the results of an SSA apply operation.
func LogApplyResults(ctx context.Context, changes []ssa.Change, manager *ssa.Manager, logFunc func(line string, args ...any)) {
	for _, change := range changes {
		switch change.Action {
		case ssa.CreatedAction, ssa.ConfiguredAction, ssa.DeletedAction:
			logFunc(" < %s %s", change.Action, change.Subject)
			logFunc("%s", change.Diff)
		case ssa.SkippedAction, ssa.UnchangedAction:
			logFunc(" > skipped %s: no changes", change.Subject)
		default:
			logFunc(" > processing manifest %s: unknown action %q", change.Subject, change.Action)
		}
	}
}

// Wait waits for the given set of changes to be fully reconciled.
func Wait(ctx context.Context, changes []ssa.Change, logFunc func(line string, args ...any), manager *ssa.Manager, waitOps ssa.WaitOptions) error {
	waitObjects := make(map[object.ObjMetadata]struct{}, len(changes))

	for _, change := range changes {
		switch change.Action {
		case ssa.CreatedAction, ssa.ConfiguredAction:
			waitObjects[change.ObjMetadata] = struct{}{}
		}
	}

	logFunc("waiting for kubernetes objects to be fully reconciled")

	err := manager.Wait(ctx,
		object.ObjMetadataSetFromMap(waitObjects),
		waitOps,
	)
	if err != nil {
		return err
	}

	logFunc("done")

	return nil
}
