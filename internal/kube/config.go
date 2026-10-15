package kube

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func loadKubeconfig(path string) (*rest.Config, error) {
	return clientcmd.BuildConfigFromFlags("", path)
}
