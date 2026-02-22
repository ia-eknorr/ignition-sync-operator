package git

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing/transport"
	gogithttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gogitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	syncv1alpha1 "github.com/ia-eknorr/ignition-sync-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// ResolveAuth reads the appropriate Secret and builds a go-git transport.AuthMethod.
// Returns nil auth (valid for public repos) if no auth is configured.
func ResolveAuth(ctx context.Context, c client.Client, namespace string, authSpec *syncv1alpha1.GitAuthSpec) (transport.AuthMethod, error) {
	if authSpec == nil {
		return nil, nil
	}

	switch {
	case authSpec.SSHKey != nil:
		return resolveSSHAuth(ctx, c, namespace, authSpec.SSHKey)
	case authSpec.Token != nil:
		return resolveTokenAuth(ctx, c, namespace, authSpec.Token)
	case authSpec.GitHubApp != nil:
		// GitHub App auth requires token exchange — deferred to a later phase.
		return nil, fmt.Errorf("GitHub App auth not yet implemented")
	default:
		return nil, nil
	}
}

func resolveSSHAuth(ctx context.Context, c client.Client, namespace string, sshAuth *syncv1alpha1.SSHKeyAuth) (transport.AuthMethod, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: sshAuth.SecretRef.Name, Namespace: namespace}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("getting SSH key secret %s/%s: %w", namespace, sshAuth.SecretRef.Name, err)
	}

	pemBytes, ok := secret.Data[sshAuth.SecretRef.Key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret %s/%s", sshAuth.SecretRef.Key, namespace, sshAuth.SecretRef.Name)
	}

	publicKey, err := gogitssh.NewPublicKeys("git", pemBytes, "")
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	// Accept any host key — in-cluster git operations typically use internal mirrors
	// or deploy keys where strict host verification is managed at the network level.
	publicKey.HostKeyCallback = ssh.InsecureIgnoreHostKey()

	return publicKey, nil
}

func resolveTokenAuth(ctx context.Context, c client.Client, namespace string, tokenAuth *syncv1alpha1.TokenAuth) (transport.AuthMethod, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: tokenAuth.SecretRef.Name, Namespace: namespace}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, fmt.Errorf("getting token secret %s/%s: %w", namespace, tokenAuth.SecretRef.Name, err)
	}

	token, ok := secret.Data[tokenAuth.SecretRef.Key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret %s/%s", tokenAuth.SecretRef.Key, namespace, tokenAuth.SecretRef.Name)
	}

	return &gogithttp.BasicAuth{
		Username: "x-access-token",
		Password: string(token),
	}, nil
}
