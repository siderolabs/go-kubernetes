// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package event

import "sigs.k8s.io/cli-utils/pkg/object"

type Type string

const (
	// ApplyType is the event type for apply events.
	ApplyType Type = "apply"

	// PruneType is the event type for prune events.
	PruneType Type = "prune"

	// RolloutType is the event type for rollout events (if objects are deployed/deleted successfully).
	RolloutType Type = "rollout"
)

// ApplyEvent describes the result of a single apply to the cluster.
type ApplyEvent struct {
	Skipped bool
}

// PruneEvent is an event for when an object is pruned from the cluster.
type PruneEvent struct{}

// RolloutEvent is the event type for rollout events (if objects are deployed/deleted successfully).
type RolloutEvent struct{}

// Event is one of event.Type.
// If error is not nil it means the action has failed.
// The event for the given event.Type is populated.
type Event struct {
	Type     Type
	Error    error
	ObjectID object.ObjMetadata

	Apply   ApplyEvent
	Prune   PruneEvent
	Rollout RolloutEvent
}
