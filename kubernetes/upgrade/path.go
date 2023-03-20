// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package upgrade

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-version"
)

// Path encodes the upgrade path.
type Path struct {
	from, to               *version.Version
	fromVersion, toVersion string
}

// NewPath creates a new upgrade path.
func NewPath(fromVersion, toVersion string) (*Path, error) {
	fromVersion = strings.TrimLeft(fromVersion, "v")
	toVersion = strings.TrimLeft(toVersion, "v")

	from, err := version.NewVersion(fromVersion)
	if err != nil {
		return nil, fmt.Errorf("error parsing from version: %w", err)
	}

	to, err := version.NewVersion(toVersion)
	if err != nil {
		return nil, fmt.Errorf("error parsing to version: %w", err)
	}

	return &Path{
		fromVersion: fromVersion,
		toVersion:   toVersion,
		from:        from,
		to:          to,
	}, nil
}

// FromVersion returns the from version.
func (p *Path) FromVersion() string {
	return p.fromVersion
}

// ToVersion returns the to version.
func (p *Path) ToVersion() string {
	return p.toVersion
}

func (p *Path) String() string {
	return fmt.Sprintf("%d.%d->%d.%d", p.from.Segments()[0], p.from.Segments()[1], p.to.Segments()[0], p.to.Segments()[1])
}

// IsSupported returns true if the upgrade path is supported.
func (p *Path) IsSupported() bool {
	switch p.String() {
	case
		"1.19->1.19",
		"1.19->1.20",
		"1.20->1.20",
		"1.20->1.21",
		"1.21->1.21",
		"1.21->1.22",
		"1.22->1.22",
		"1.22->1.23",
		"1.23->1.23",
		"1.23->1.24",
		"1.24->1.24",
		"1.24->1.25",
		"1.25->1.25",
		"1.25->1.26",
		"1.26->1.26",
		"1.26->1.27",
		"1.27->1.27":
		return true
	}

	return false
}
