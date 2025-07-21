# Node.js Package for Buildkite MCP Server

This directory contains the Node.js package implementation for the Buildkite MCP Server. The package provides an easy way to install and use the Buildkite MCP Server binary in Node.js projects.

## Files

- `install.js`: Installation script that automatically downloads the appropriate Buildkite MCP Server binary for the user's platform and architecture. It handles platform detection, downloading from GitHub releases, and extraction of compressed archives.

- `local.js`: Testing script to verify the package structure and installation process locally before publishing. It performs several checks including file existence, making scripts executable, and testing the install script.

- `bin/`: Directory containing executable files for the MCP server.

## Usage

This package is published to npm as `@buildkite/buildkite-mcp-server`. When installed, it automatically downloads the appropriate binary for the user's platform.

### Installation

```bash
npm install @buildkite/buildkite-mcp-server
```

### Running with npx

You can also run the package directly using `npx` without installing it:

```bash
npx @buildkite/buildkite-mcp-server
```

### Authentication

The Buildkite MCP Server requires authentication with your Buildkite API token. You can provide this in two ways:

1. Set the `BUILDKITE_API_TOKEN` environment variable:
   ```bash
   export BUILDKITE_API_TOKEN=your-api-token
   ```

2. Use the `--token` command line option:
   ```bash
   npx @buildkite/buildkite-mcp-server --token=your-api-token
   ```

### Development

To test the package locally:

1. Run the local testing script:
   ```bash
   node local.js
   ```

2. Create a test package and install it globally:
   ```bash
   npm pack
   npm install -g ./buildkite-buildkite-mcp-server-*.tgz
   ```

3. Test the installed binary:
   ```bash
   buildkite-mcp-server --help
   ```

## Contributing

If you're making changes to this package, please ensure you test thoroughly using the steps above before submitting a pull request.
