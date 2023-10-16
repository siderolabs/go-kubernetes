// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package kubernetes

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isPlatformRetryableError(err error) bool {
	switch {
	case errors.Is(err, windows.WSAECONNRESET), errors.Is(err, windows.WSAECONNABORTED), errors.Is(err, windows.WSAECONNREFUSED):
		return true
	default:
		return false
	}
}
