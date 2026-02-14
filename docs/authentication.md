# Authentication Flow

[← Back to README](../README.md)

The extension leverages Azure CLI credentials on your local machine to authenticate with Azure DevOps:

1. A Node.js service using the `@azure/identity` package connects to your Azure CLI credentials
2. An SSH connection forwards this service to a Unix socket in the codespace
3. Development tools inside the codespace request tokens through the ADO Auth Helper
