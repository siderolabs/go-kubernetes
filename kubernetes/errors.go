// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package kubernetes

import (
	"errors"
	"io"
	"net"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// IsRetryableError returns true if this Kubernetes API should be retried.
func IsRetryableError(err error) bool {
	if apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsInternalError(err) {
		return true
	}

	for _, retryableError := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNREFUSED, syscall.ECONNRESET} {
		if errors.Is(err, retryableError) {
			return true
		}
	}

	if isPlatformRetryableError(err) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		// https://groups.google.com/g/golang-nuts/c/-JcZzOkyqYI/m/xwaZzjCgAwAJ
		if netErr.Temporary() || netErr.Timeout() { //nolint:staticcheck
			return true
		}
	}

	return false
}
