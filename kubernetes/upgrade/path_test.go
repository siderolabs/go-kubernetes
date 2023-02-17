// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/siderolabs/go-kubernetes/kubernetes/upgrade"
)

func TestPath(t *testing.T) {
	p, err := upgrade.NewPath("v1.19.5", "1.20.7")
	require.NoError(t, err)

	assert.Equal(t, "1.19.5", p.FromVersion())
	assert.Equal(t, "1.20.7", p.ToVersion())

	assert.Equal(t, "1.19->1.20", p.String())

	assert.True(t, p.IsSupported())
}
