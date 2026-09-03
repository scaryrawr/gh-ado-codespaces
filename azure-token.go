package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"unicode"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const defaultADOScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

type tokenTargetKind uint8

const (
	tokenTargetResource tokenTargetKind = iota + 1
	tokenTargetScopes
)

type tokenTarget struct {
	kind   tokenTargetKind
	values []string
}

func parseTokenTarget(raw *string) (tokenTarget, error) {
	if raw == nil {
		return tokenTarget{kind: tokenTargetResource, values: []string{defaultADOScope}}, nil
	}
	if len(*raw) > 16*1024 {
		return tokenTarget{}, errors.New("scope request exceeds 16 KiB")
	}
	for _, char := range *raw {
		if unicode.IsControl(char) {
			return tokenTarget{}, errors.New("scope request contains control characters")
		}
		if unicode.IsSpace(char) && char != ' ' {
			return tokenTarget{}, errors.New("scope request contains unsupported whitespace")
		}
	}
	if strings.Trim(*raw, " ") == "" {
		return tokenTarget{kind: tokenTargetResource, values: []string{defaultADOScope}}, nil
	}

	values := strings.Fields(*raw)
	if len(values) > 64 {
		return tokenTarget{}, errors.New("scope request exceeds 64 scopes")
	}
	for _, value := range values {
		if len(value) > 2*1024 {
			return tokenTarget{}, errors.New("scope exceeds 2 KiB")
		}
		if strings.HasPrefix(value, "-") {
			return tokenTarget{}, fmt.Errorf("scope %q cannot begin with '-'", value)
		}
	}

	kind := tokenTargetScopes
	if len(values) == 1 && strings.HasSuffix(values[0], "/.default") {
		kind = tokenTargetResource
	}

	return tokenTarget{kind: kind, values: values}, nil
}

type tokenProvider interface {
	GetAccessToken(context.Context, tokenTarget) (string, error)
}

type runAzureCLI func(context.Context, string, ...string) ([]byte, error)

type azureTokenProvider struct {
	resourceCredential azcore.TokenCredential
	subscription       string
	run                runAzureCLI
	mu                 sync.Mutex
}

func newAzureTokenProvider(subscription string) (*azureTokenProvider, error) {
	subscription = strings.TrimSpace(subscription)
	var options *azidentity.AzureCLICredentialOptions
	if subscription != "" {
		options = &azidentity.AzureCLICredentialOptions{Subscription: subscription}
	}

	credential, err := azidentity.NewAzureCLICredential(options)
	if err != nil {
		return nil, err
	}

	return &azureTokenProvider{
		resourceCredential: credential,
		subscription:       subscription,
		run:                executeAzureCLI,
	}, nil
}

func (p *azureTokenProvider) GetAccessToken(ctx context.Context, target tokenTarget) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch target.kind {
	case tokenTargetResource:
		token, err := p.resourceCredential.GetToken(ctx, policy.TokenRequestOptions{Scopes: target.values})
		if err != nil {
			return "", err
		}

		return token.Token, nil
	case tokenTargetScopes:
		args := []string{
			"account",
			"get-access-token",
			"--only-show-errors",
			"--output",
			"tsv",
			"--query",
			"accessToken",
		}
		if p.subscription != "" {
			args = append(args, "--subscription", p.subscription)
		}
		args = append(args, "--scope")
		args = append(args, target.values...)

		output, err := p.run(ctx, "az", args...)
		if err != nil {
			return "", fmt.Errorf("get scoped Azure CLI token: %w", err)
		}

		token := strings.TrimSpace(string(output))
		if token == "" {
			return "", errors.New("Azure CLI returned an empty access token")
		}
		if strings.IndexFunc(token, unicode.IsSpace) >= 0 {
			return "", errors.New("Azure CLI returned an invalid access token")
		}

		return token, nil
	default:
		return "", errors.New("invalid token target")
	}
}

func executeAzureCLI(ctx context.Context, executable string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, args...)
	output, err := command.Output()
	if err == nil {
		return output, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		message := strings.TrimSpace(string(exitErr.Stderr))
		if message != "" {
			return nil, fmt.Errorf("%s: %w", message, err)
		}
	}

	return nil, err
}
