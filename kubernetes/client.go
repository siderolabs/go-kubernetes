// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package kubernetes provides helpers for the Kubernetes API client.
package kubernetes

import (
	"fmt"
	"net"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/connrotation"
)

// Client wraps the Kubernetes API client providing a way to force close all connections.
type Client struct {
	*kubernetes.Clientset

	dialer *connrotation.Dialer
}

// NewDialer creates new custom dialer.
func NewDialer() *connrotation.Dialer {
	return connrotation.NewDialer((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
}

// NewForConfig initializes and returns a client using the provided config.
func NewForConfig(config *rest.Config) (*Client, error) {
	if config.Dial != nil {
		return nil, fmt.Errorf("dialer is already set")
	}

	dialer := NewDialer()
	config.Dial = dialer.DialContext

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &Client{
		Clientset: clientset,
		dialer:    dialer,
	}, nil
}

// Close all connections.
func (h *Client) Close() error {
	h.dialer.CloseAll()

	return nil
}
