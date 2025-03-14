// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package compatibility provides some a way to enable/disable features based on Kubernetes version.
package compatibility

import (
	"strings"

	"github.com/blang/semver/v4"
	"github.com/google/go-containerregistry/pkg/name"
)

// Version is the Kubernetes version to have running.
type Version semver.Version

// String returns the string representation of the version.
func (v Version) String() string {
	return semver.Version(v).String()
}

// latest is used if the version can't be parsed.
var latest = Version{
	Major: 1,
	Minor: 99,
}

// VersionFromImageRef parses container image ref to return just Kubernetes version.
//
// If the version can't be parsed, assume latest version.
func VersionFromImageRef(imageRef string) Version {
	// try to parse as tagged
	ref, err := name.NewTag(imageRef)
	if err != nil {
		// try to cut digest part
		var ok bool

		imageRef, _, ok = strings.Cut(imageRef, "@")

		if ok {
			ref, err = name.NewTag(imageRef)
		}

		if err != nil {
			return latest
		}
	}

	v, err := semver.ParseTolerant(ref.TagStr())
	if err != nil {
		return latest
	}

	return Version(semver.Version{
		Major: v.Major,
		Minor: v.Minor,
		Patch: v.Patch,
	})
}
