package kubernetes

import (
	// Kubernetes clients
	// Ref: https://pkg.go.dev/k8s.io/client-go/dynamic
	_ "k8s.io/client-go/dynamic"
	kubernetes "k8s.io/client-go/kubernetes"
	// Ref: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/client/config
	ctrl "sigs.k8s.io/controller-runtime"
)

// NewClient return a new Kubernetes client from client-go SDK
func NewClient() (client *kubernetes.Clientset, err error) {
	config, err := ctrl.GetConfig()
	if err != nil {
		return client, err
	}

	// Create the clients to do requests to our friend: Kubernetes
	client, err = kubernetes.NewForConfig(config)
	if err != nil {
		return client, err
	}

	return client, err
}
