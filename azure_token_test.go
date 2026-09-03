package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestParseTokenTarget(t *testing.T) {
	tests := []struct {
		name       string
		raw        *string
		wantKind   tokenTargetKind
		wantValues []string
		wantError  bool
	}{
		{name: "missing uses ADO default", wantKind: tokenTargetResource, wantValues: []string{defaultADOScope}},
		{name: "empty uses ADO default", raw: stringPointer(""), wantKind: tokenTargetResource, wantValues: []string{defaultADOScope}},
		{name: "resource default", raw: stringPointer("https://management.azure.com/.default"), wantKind: tokenTargetResource, wantValues: []string{"https://management.azure.com/.default"}},
		{name: "single delegated scope", raw: stringPointer("https://graph.microsoft.com/User.Read"), wantKind: tokenTargetScopes, wantValues: []string{"https://graph.microsoft.com/User.Read"}},
		{name: "multiple scopes", raw: stringPointer("scope-a   scope-b"), wantKind: tokenTargetScopes, wantValues: []string{"scope-a", "scope-b"}},
		{name: "control character", raw: stringPointer("scope-a\tscope-b"), wantError: true},
		{name: "control-only input", raw: stringPointer("\t"), wantError: true},
		{name: "non-ASCII whitespace", raw: stringPointer("scope-a\u00a0scope-b"), wantError: true},
		{name: "option-like scope", raw: stringPointer("--subscription"), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := parseTokenTarget(tt.raw)
			if tt.wantError {
				if err == nil {
					t.Fatal("parseTokenTarget() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseTokenTarget() error = %v", err)
			}
			if target.kind != tt.wantKind {
				t.Fatalf("parseTokenTarget() kind = %v, want %v", target.kind, tt.wantKind)
			}
			if !reflect.DeepEqual(target.values, tt.wantValues) {
				t.Fatalf("parseTokenTarget() values = %v, want %v", target.values, tt.wantValues)
			}
		})
	}
}

func TestAzureTokenProviderRoutesResourceTarget(t *testing.T) {
	credential := &recordingTokenCredential{token: "resource-token"}
	provider := &azureTokenProvider{
		resourceCredential: credential,
		run: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("Azure CLI runner called for resource target")
			return nil, nil
		},
	}
	target := tokenTarget{kind: tokenTargetResource, values: []string{"https://graph.microsoft.com/.default"}}

	token, err := provider.GetAccessToken(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if token != "resource-token" {
		t.Fatalf("token = %q, want resource-token", token)
	}
	if !reflect.DeepEqual(credential.scopes, target.values) {
		t.Fatalf("credential scopes = %v, want %v", credential.scopes, target.values)
	}
}

func TestAzureTokenProviderBuildsMultiScopeCLIRequest(t *testing.T) {
	var executable string
	var args []string
	provider := &azureTokenProvider{
		subscription: "subscription-id",
		run: func(_ context.Context, gotExecutable string, gotArgs ...string) ([]byte, error) {
			executable = gotExecutable
			args = append([]string(nil), gotArgs...)
			return []byte("scope-token\n"), nil
		},
	}
	target := tokenTarget{kind: tokenTargetScopes, values: []string{"scope-a", "scope-b"}}

	token, err := provider.GetAccessToken(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if token != "scope-token" {
		t.Fatalf("token = %q, want scope-token", token)
	}
	if executable != "az" {
		t.Fatalf("executable = %q, want az", executable)
	}
	wantArgs := []string{
		"account", "get-access-token",
		"--only-show-errors",
		"--output", "tsv",
		"--query", "accessToken",
		"--subscription", "subscription-id",
		"--scope", "scope-a", "scope-b",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %v, want %v", args, wantArgs)
	}
}

func TestAzureTokenProviderKeepsScopeArgumentsOpaque(t *testing.T) {
	var args []string
	provider := &azureTokenProvider{
		run: func(_ context.Context, _ string, gotArgs ...string) ([]byte, error) {
			args = append([]string(nil), gotArgs...)
			return []byte("scope-token\n"), nil
		},
	}
	target, err := parseTokenTarget(stringPointer("api://example/read;echo api://example/$value"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := provider.GetAccessToken(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"--scope", "api://example/read;echo", "api://example/$value"}
	if !reflect.DeepEqual(args[len(args)-len(wantTail):], wantTail) {
		t.Fatalf("scope args = %v, want %v", args[len(args)-len(wantTail):], wantTail)
	}
}

func TestAzureTokenProviderRejectsInvalidCLIOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{name: "empty token"},
		{name: "token with whitespace", output: "not a token"},
		{name: "runner error", err: errors.New("failed")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &azureTokenProvider{
				run: func(context.Context, string, ...string) ([]byte, error) {
					return []byte(tt.output), tt.err
				},
			}

			_, err := provider.GetAccessToken(context.Background(), tokenTarget{
				kind:   tokenTargetScopes,
				values: []string{"scope-a"},
			})
			if err == nil {
				t.Fatal("GetAccessToken() error = nil, want error")
			}
		})
	}
}

type recordingTokenCredential struct {
	scopes []string
	token  string
}

func (c *recordingTokenCredential) GetToken(_ context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.scopes = append([]string(nil), options.Scopes...)

	return azcore.AccessToken{Token: c.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func stringPointer(value string) *string {
	return &value
}
