// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"

	"k8s.io/client-go/rest"
)

// SyncWithLog applies the manifests to the cluster logging the results via logFunc.
func SyncWithLog(ctx context.Context, objects []Manifest, config *rest.Config, dryRun bool, logFunc func(string, ...any)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	syncCh := make(chan SyncResult)
	errCh := make(chan error, 1)

	go func() {
		errCh <- Sync(ctx, objects, config, dryRun, syncCh)
	}()

	logFunc("updating manifests")

	var updatedManifests []Manifest

syncLoop:
	for {
		select {
		case result := <-syncCh:
			logFunc(" > processing manifest %s", result.Path)

			switch {
			case result.Skipped:
				logFunc(" < no changes")
			case dryRun:
				logFunc("%s", result.Diff)
				logFunc(" < dry run, change skipped")
			case !dryRun:
				logFunc("%s", result.Diff)
				logFunc(" < applied successfully")

				updatedManifests = append(updatedManifests, result.Object)
			}
		case err := <-errCh:
			if err == nil {
				break syncLoop
			}

			return err
		}
	}

	if dryRun {
		return nil
	}

	logFunc("waiting for all manifests to be applied")

	rolloutCh := make(chan RolloutProgress)

	go func() {
		errCh <- WaitForRollout(ctx, config, updatedManifests, rolloutCh)
	}()

	for {
		select {
		case result := <-rolloutCh:
			logFunc(" > waiting for %s", result.Path)
		case err := <-errCh:
			return err
		}
	}
}
