#!/usr/bin/env node

// Helper script for Azure DevOps authentication.
// This script connects to the forwarded local authentication socket and
// returns tokens in Git credential helper format.

const fs = require("fs");
const net = require("net");
const path = require("path");

function readStdin() {
  return fs.readFileSync(0, "utf8");
}

function requestAccessToken(socketPath, scopes) {
  return new Promise((resolve) => {
    let responseData = "";
    let settled = false;

    const socket = net.createConnection({ path: socketPath }, () => {
      const request = {
        type: "getAccessToken",
        data: scopes ? { scopes } : {},
      };
      socket.write(`${JSON.stringify(request)}\f`);
    });

    const finish = (token) => {
      if (settled) {
        return;
      }

      settled = true;
      socket.destroy();
      resolve(token);
    };

    socket.setTimeout(60000);
    socket.on("data", (chunk) => {
      responseData += chunk.toString("utf8");
      const delimiterIndex = responseData.indexOf("\f");
      if (delimiterIndex === -1) {
        return;
      }

      try {
        const response = JSON.parse(responseData.slice(0, delimiterIndex));
        if (response.type === "accessToken" && typeof response.data === "string") {
          finish(response.data);
          return;
        }
      } catch {
        // Invalid responses are treated the same as unavailable sockets.
      }

      finish(null);
    });
    socket.on("error", () => finish(null));
    socket.on("end", () => finish(null));
    socket.on("timeout", () => finish(null));
  });
}

async function getAccessToken(scopes) {
  let socketPaths;
  if (process.env.GH_ADO_CODESPACES_AUTH_SOCKET) {
    socketPaths = [process.env.GH_ADO_CODESPACES_AUTH_SOCKET];
  } else {
    try {
      socketPaths = fs
        .readdirSync("/tmp")
        .filter((filename) => filename.startsWith("ado-auth-") && filename.endsWith(".sock"))
        .map((filename) => path.join("/tmp", filename));
    } catch {
      socketPaths = [];
    }
  }

  for (const socketPath of socketPaths) {
    const token = await requestAccessToken(socketPath, scopes);
    if (token) {
      return token;
    }
  }

  return null;
}

function isGitAskingForADORepo() {
  const input = readStdin().toLowerCase();
  return input.includes("dev.azure.com") || input.includes(".visualstudio.com");
}

async function main() {
  if (process.argv.length < 3) {
    process.exitCode = 1;
    return;
  }

  const command = process.argv[2];
  if (command === "get") {
    if (!isGitAskingForADORepo()) {
      return;
    }

    const token = await getAccessToken();
    if (!token) {
      process.exitCode = 1;
      return;
    }

    process.stdout.write(`username=token\npassword=${token}\n`);
    return;
  }

  if (command === "get-access-token") {
    const scriptName = path.basename(process.argv[1]);
    const scopeArgs = scriptName === "azure-auth-helper" ? process.argv.slice(3) : [];
    const scopes = scopeArgs.length > 0 ? scopeArgs.join(" ") : undefined;
    const token = await getAccessToken(scopes);
    if (!token) {
      process.exitCode = 1;
      return;
    }

    process.stdout.write(`${token}\n`);
  }
}

main().catch(() => {
  process.exitCode = 1;
});
