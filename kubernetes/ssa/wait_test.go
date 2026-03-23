// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// nolint: contextcheck,godoclint
package ssa_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fluxcd/cli-utils/pkg/object"
	fluxssa "github.com/fluxcd/pkg/ssa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/siderolabs/go-kubernetes/kubernetes/ssa"
	"github.com/siderolabs/go-kubernetes/kubernetes/ssa/internal/resourcemanager"
)

// retryableWaitResourceManager returns a retryable error for the first N calls
// to WaitForSetWithContext, then succeeds.
type retryableWaitResourceManager struct {
	resourcemanager.Mock
	remaining atomic.Int32
}

func (m *retryableWaitResourceManager) WaitForSetWithContext(_ context.Context, _ object.ObjMetadataSet, _ fluxssa.WaitOptions) error {
	if m.remaining.Add(-1) >= 0 {
		return apierrors.NewInternalError(errors.New("transient API server error"))
	}

	return nil
}

func TestWait(t *testing.T) {
	t.Run("retries_on_transient_error_then_succeeds", func(t *testing.T) {
		rm := &retryableWaitResourceManager{}
		rm.remaining.Store(2) // fail twice, succeed on third

		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		err := manager.Wait(t.Context(), object.ObjMetadataSet{}, ssa.WaitOptions{
			Timeout:  30 * time.Second,
			Interval: 1 * time.Second,
		})
		require.NoError(t, err)

		// remaining should be -1
		assert.Equal(t, rm.remaining.Load(), int32(-1))
	})

	t.Run("respects_timeout_budget", func(t *testing.T) {
		// Always returns a retryable error — Wait must still return within the timeout.
		rm := &retryableWaitResourceManager{}
		rm.remaining.Store(1000) // never succeeds

		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		timeout := 3 * time.Second

		start := time.Now()

		err := manager.Wait(t.Context(), object.ObjMetadataSet{}, ssa.WaitOptions{
			Timeout:  timeout,
			Interval: 500 * time.Millisecond,
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Less(t, elapsed, timeout+100*time.Millisecond, "Wait should not exceed timeout by more than a small margin")
	})

	t.Run("non_retryable_error_returns_immediately", func(t *testing.T) {
		rm := &permanentWaitFailResourceManager{err: errors.New("resources failed")}

		manager := ssa.NewCustomManager(rm, testInventoryFactory, nil, &mapperMock{})

		start := time.Now()

		err := manager.Wait(t.Context(), object.ObjMetadataSet{}, ssa.WaitOptions{
			Timeout:  30 * time.Second,
			Interval: 1 * time.Second,
		})
		elapsed := time.Since(start)

		require.ErrorContains(t, err, "resources failed")
		assert.Less(t, elapsed, 2*time.Second, "non-retryable error should return immediately")
	})
}

// permanentWaitFailResourceManager always returns a non-retryable error.
type permanentWaitFailResourceManager struct {
	resourcemanager.Mock
	err error
}

func (m *permanentWaitFailResourceManager) WaitForSetWithContext(_ context.Context, _ object.ObjMetadataSet, _ fluxssa.WaitOptions) error {
	return m.err
}
