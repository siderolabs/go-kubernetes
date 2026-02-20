// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package ssa

import (
	"context"

	"github.com/fluxcd/cli-utils/pkg/object"
	"github.com/fluxcd/pkg/ssa"
)

func (m *Manager) Wait(ctx context.Context, set object.ObjMetadataSet, opts ssa.WaitOptions) error {
	return m.resourceManager.WaitForSetWithContext(ctx, set, opts)
}
