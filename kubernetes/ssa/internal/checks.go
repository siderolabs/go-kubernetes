// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package internal contains inventory and resource manager implementations.
package internal

import (
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/configmap"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/inventory/memory"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/resourcemanager"
)

var (
	_ ssa.Inventory       = (*memory.Inventory)(nil)
	_ ssa.Inventory       = (*configmap.Inventory)(nil)
	_ ssa.ResourceManager = (*resourcemanager.Mock)(nil)
)
