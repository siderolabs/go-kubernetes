// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package kubernetes provides helpers for the Kubernetes API client.
package kubernetes

import (
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client wraps the Kubernetes API client providing a way to close idle connections.
type Client struct {
	*kubernetes.Clientset
	httpClient *http.Client
}

// NewForConfig initializes and returns a client using the provided config.
func NewForConfig(config *rest.Config) (*Client, error) {
	// rest.HTTPClientFor builds the *http.Client with TLS + auth configured,
	// going through the transport cache normally (stable cache key).
	httpClient, err := rest.HTTPClientFor(config)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfigAndClient(config, httpClient)
	if err != nil {
		return nil, err
	}

	return &Client{
		Clientset:  clientset,
		httpClient: httpClient,
	}, nil
}

// Close closes idle connections.
func (h *Client) Close() error {
	h.httpClient.CloseIdleConnections()

	return nil
}
