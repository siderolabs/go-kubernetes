// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests

import (
	"context"
	"fmt"
	"slices"

	"k8s.io/client-go/rest"

	"github.com/siderolabs/go-kubernetes/kubernetes/manifests/event"
)

// SyncWithLogSSA applies the manifests to the cluster via ssa and logs the results via logFunc.
func SyncWithLogSSA(
	ctx context.Context,
	objects []Manifest,
	config *rest.Config,
	ops SSAOptions,
	logFunc func(string, ...any),
) error {
	syncCh := make(chan event.Event)
	errCh := make(chan error, 1)

	go func() {
		errCh <- SyncSSA(ctx, objects, config, syncCh, ops)
	}()

	waitMsgPrintedObjects := []string{}

syncLoop:
	for {
		select {
		case e := <-syncCh:
			objPath := fmt.Sprintf("%s %s/%s", e.ObjectID.GroupKind.Kind, e.ObjectID.Namespace, e.ObjectID.Name)
			if e.ObjectID.Namespace == "" {
				objPath = fmt.Sprintf("%s %s", e.ObjectID.GroupKind.Kind, e.ObjectID.Name)
			}

			if e.Type == event.RolloutType && e.Error == nil && !slices.Contains(waitMsgPrintedObjects, objPath) {
				// Skip printing successful rollout statuses unless a "waiting for" message was printed for them previously
				// to reduce spam.
				continue
			}

			if e.Type == event.WaitType {
				logFunc("> waiting for %s", objPath)
				waitMsgPrintedObjects = append(waitMsgPrintedObjects, objPath)

				continue
			}

			if e.Error != nil {
				logFunc("< %s of %s failed: %s", e.Type, objPath, e.Error.Error())
			} else {
				logFunc("< %s of %s successful", e.Type, objPath)
			}

		case err := <-errCh:
			if err == nil {
				break syncLoop
			}

			return err
		}
	}

	return nil
}

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
