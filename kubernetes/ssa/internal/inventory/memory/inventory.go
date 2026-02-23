// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package memory implements an in-memory inventory for unit tests.
package memory

import (
	"context"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/object"
)

// Inventory is an in-memory implementation of ssa.Inventory for unit tests.
type Inventory struct {
	id   string
	refs object.ObjMetadataSet
}

// NewInventory creates a new in-memory inventory.
func NewInventory(id string) *Inventory {
	return &Inventory{id: id}
}

func (i *Inventory) ID() string {
	return i.id
}

func (i *Inventory) Get() object.ObjMetadataSet {
	return i.refs
}

func (i *Inventory) Update(refs object.ObjMetadataSet) {
	i.refs = refs
}

func (i *Inventory) Write(context.Context) error {
	return nil
}

func (i *Inventory) Delete(context.Context) error {
	i.refs = nil

	return nil
}
