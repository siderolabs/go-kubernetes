// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"github.com/go-logr/logr"
)

// WaitOptions contains options for wait requests.
type WaitOptions = ssa.WaitOptions

// Wait checks if the given set of objects has been fully reconciled.
func (m *Manager) Wait(ctx context.Context, set object.ObjMetadataSet, ops WaitOptions) error {
	ctx = logr.NewContext(ctx, logr.FromContextOrDiscard(ctx))

	if ops.Interval == 0 {
		ops.Interval = ssa.DefaultApplyOptions().WaitInterval
	}

	if ops.Timeout == 0 {
		ops.Timeout = ssa.DefaultApplyOptions().WaitTimeout
	}

	return m.resourceManager.WaitForSetWithContext(ctx, set, ops)
}
