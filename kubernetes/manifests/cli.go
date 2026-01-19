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

// SyncAndDiffWithLogSSA prints the diff and then runs SyncWithLogSSA.
func SyncAndDiffWithLogSSA(
	ctx context.Context,
	objects []Manifest,
	config *rest.Config,
	ops SSAOptions,
	logFunc func(string, ...any),
) error {
	logFunc("comparing with live objects")

	result, err := DiffSSA(ctx, objects, config, ops)
	if err != nil {
		return err
	}

	LogSSADiff(result, logFunc)

	logFunc("applying manifests")

	return SyncWithLogSSA(
		ctx,
		objects,
		config,
		ops,
		logFunc,
	)
}

// LogSSADiff logs the SSA diff results via logfunc in a human readable format.
func LogSSADiff(result []DiffResult, logFunc func(string, ...any)) {
	if len(result) == 0 {
		logFunc("< no changes detected")
	}

	for _, r := range result {
		objPath := FormatObjectPath(r.Object)

		logFunc("< %s %s", r.Action, objPath)
		logFunc("%s", r.Diff)
	}
}

// FormatObjectPath returns the object path (e.g. "Deployment kube-system/my-deploy").
func FormatObjectPath(object Manifest) string {
	objPath := fmt.Sprintf("%s %s/%s", object.GroupVersionKind().Kind, object.GetNamespace(), object.GetName())
	if object.GetNamespace() == "" {
		objPath = fmt.Sprintf("%s %s", object.GroupVersionKind().Kind, object.GetName())
	}

	return objPath
}

// NewSyncEventLogger creates a new syncEventLogger, which is a helper to log incoming sync event via logFunc.
func NewSyncEventLogger(logFunc func(string, ...any)) syncEventLogger {
	return syncEventLogger{
		logFunc: logFunc,
	}
}

type syncEventLogger struct {
	logFunc               func(string, ...any)
	waitMsgPrintedObjects []string
}

// LogSyncEvent logs important incoming sync events in a human readable manner.
func (sel *syncEventLogger) LogSyncEvent(e event.Event) {
	objPath := fmt.Sprintf("%s %s/%s", e.ObjectID.GroupKind.Kind, e.ObjectID.Namespace, e.ObjectID.Name)
	if e.ObjectID.Namespace == "" {
		objPath = fmt.Sprintf("%s %s", e.ObjectID.GroupKind.Kind, e.ObjectID.Name)
	}

	if e.Type == event.RolloutType && e.Error == nil && !slices.Contains(sel.waitMsgPrintedObjects, objPath) {
		// Skip printing successful rollout statuses unless a "waiting for" message was printed for them previously
		// to reduce spam.
		return
	}

	if e.Type == event.WaitType {
		sel.logFunc("> waiting for %s", objPath)
		sel.waitMsgPrintedObjects = append(sel.waitMsgPrintedObjects, objPath)

		return
	}

	if e.Error != nil {
		sel.logFunc("< %s of %s failed: %s", e.Type, objPath, e.Error.Error())
	} else {
		sel.logFunc("< %s of %s successful", e.Type, objPath)
	}
}

func (sel *syncEventLogger) Reset() {
	sel.waitMsgPrintedObjects = []string{}
}

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

	eventLogger := NewSyncEventLogger(logFunc)

syncLoop:
	for {
		select {
		case e := <-syncCh:
			eventLogger.LogSyncEvent(e)

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
