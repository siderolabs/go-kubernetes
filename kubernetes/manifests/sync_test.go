// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package manifests //nolint:testpackage

import (
	"strings"
	"testing"

	"github.com/fluxcd/pkg/ssa/utils"
	"github.com/stretchr/testify/assert"
)

func TestGetManifestPath(t *testing.T) {
	nsManifest := `
apiVersion: v123
kind: ConfigMap
metadata:
  name: app-config
  namespace: test-lab
data:
  APP_MESSAGE: "hello"`

	resource, err := utils.ReadObject(strings.NewReader(nsManifest))
	assert.NoError(t, err)

	result := getManifestPath(resource)
	assert.Equal(t, "v123.ConfigMap/test-lab/app-config", result)
}

func TestGetManifestPathNamespace(t *testing.T) {
	nsManifest := `
apiVersion: v1
kind: Namespace
metadata:
  name: test-lab`

	resource, err := utils.ReadObject(strings.NewReader(nsManifest))
	assert.NoError(t, err)

	result := getManifestPath(resource)
	assert.Equal(t, "v1.Namespace/test-lab", result)
}
