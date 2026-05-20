package kubeclient

import (
	"context"
	"errors"
	"strings"
	"time"

	"novaobs/internal/platform/authctx"
	platformrbac "novaobs/internal/platform/rbac"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	ErrClusterRequired = errors.New("k8s_cluster_required")
)

type CredentialReader interface {
	Kubeconfig(ctx context.Context, subject platformrbac.Subject, clusterID string) ([]byte, error)
}

type ClientsetProvider interface {
	Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error)
}

type Provider struct {
	credentials CredentialReader
	timeout     time.Duration
}

func NewProvider(credentials CredentialReader) Provider {
	return Provider{credentials: credentials, timeout: 15 * time.Second}
}

func (p Provider) Clientset(ctx context.Context, clusterID string) (kubernetes.Interface, error) {
	clusterID = strings.TrimSpace(clusterID)
	if clusterID == "" {
		return nil, ErrClusterRequired
	}
	subject, _ := authctx.SubjectFrom(ctx)
	kubeconfig, err := p.credentials.Kubeconfig(ctx, subject, clusterID)
	if err != nil {
		return nil, err
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, err
	}
	if config.Timeout == 0 {
		config.Timeout = p.timeout
	}
	return kubernetes.NewForConfig(config)
}
