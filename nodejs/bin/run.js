#!/usr/bin/env node

const { spawn } = require("child_process");
const path = require("path");
const os = require("os");
const fs = require("fs");

const binName =
  os.platform() === "win32"
    ? "buildkite-mcp-server.exe"
    : "buildkite-mcp-server";
const binPath = path.join(__dirname, binName);

// Check if binary exists
if (!fs.existsSync(binPath)) {
  console.error(`Binary not found at ${binPath}`);
  console.error("Try running: npm install");
  process.exit(1);
}

// Forward all arguments to the binary
const proc = spawn(binPath, process.argv.slice(2), {
  stdio: "inherit",
  env: process.env,
});

proc.on("exit", (code) => {
  process.exit(code || 0);
});

proc.on("error", (err) => {
  console.error("Error running buildkite-mcp-server:", err);
  process.exit(1);
});
