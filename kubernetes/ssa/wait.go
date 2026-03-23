// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
	"github.com/go-logr/logr"
	"github.com/siderolabs/go-retry/retry"

	"github.com/siderolabs/go-kubernetes/kubernetes"
)

// WaitOptions contains options for wait requests.
type WaitOptions = ssa.WaitOptions

// Wait checks if the given set of objects has been fully reconciled.
//
// The total wait time is bound by ops.Timeout. Transient network errors
// (connection resets, API server timeouts) are retried within that budget.
func (m *Manager) Wait(ctx context.Context, set object.ObjMetadataSet, ops WaitOptions) error {
	ctx = logr.NewContext(ctx, logr.FromContextOrDiscard(ctx))

	if ops.Interval == 0 {
		ops.Interval = ssa.DefaultApplyOptions().WaitInterval
	}

	if ops.Timeout == 0 {
		ops.Timeout = ssa.DefaultApplyOptions().WaitTimeout
	}

	return retry.Constant(ops.Timeout, retry.WithUnits(ops.Interval), retry.WithErrorLogging(true)).RetryWithContext(ctx, func(ctx context.Context) error {
		err := m.resourceManager.WaitForSetWithContext(ctx, set, ops)

		if kubernetes.IsRetryableError(err) {
			return retry.ExpectedError(err)
		}

		return err
	})
}
