# Authentication Flow

[← Back to README](../README.md)

The extension leverages Azure CLI credentials on your local machine to authenticate with Azure DevOps:

1. A Node.js service using the `@azure/identity` package connects to your Azure CLI credentials
2. An SSH connection forwards this service to a Unix socket in the codespace
3. Development tools inside the codespace request tokens through the ADO Auth Helper

The `azure-auth-helper` path supports the resource and scope requests emitted by the
Codespaces artifacts helper:

- Requests without a resource or scope use the Azure DevOps `/.default` scope.
- Resource and resource-type requests remain on the Azure CLI resource flow. Assigned
  application roles are included by Microsoft Entra ID through `/.default`.
- Explicit delegated scopes are forwarded to `az account get-access-token --scope`.
  Multiple scopes are passed as separate Azure CLI arguments.

The current upstream `codespace-features` `az` shim only retains one argument following
`--scope`. Until that shim accumulates all `nargs='*'` values, quote multiple scopes so
they reach `azure-auth-helper` as one value:

```bash
az account get-access-token --scope "api://example/read api://example/write"
```
