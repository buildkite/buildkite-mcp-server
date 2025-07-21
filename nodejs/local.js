#!/usr/bin/env node

const fs = require("fs");
const path = require("path");

console.log("🧪 Testing buildkite-mcp-server package locally...\n");

// Check if we're in the right directory
const packageJsonPath = path.join(process.cwd(), "package.json");
if (!fs.existsSync(packageJsonPath)) {
  console.error(
    "❌ package.json not found. Run this from the package root directory.",
  );
  process.exit(1);
}

const packageJson = JSON.parse(fs.readFileSync(packageJsonPath, "utf8"));
if (packageJson.name !== "@buildkite/buildkite-mcp-server") {
  console.error(
    "❌ Wrong package. This script is for @buildkite/buildkite-mcp-server",
  );
  process.exit(1);
}

// Test 1: Check all required files exist
console.log("📁 Checking required files...");
const requiredFiles = [
  { path: path.join(__dirname, "install.js"), display: "install.js" },
  { path: path.join(__dirname, "bin/run.js"), display: "bin/run.js" },
];

for (const file of requiredFiles) {
  if (fs.existsSync(file.path)) {
    console.log(`✅ ${file.display}`);
  } else {
    console.log(`❌ ${file.display} - Missing!`);
    process.exit(1);
  }
}

// Test 2: Make bin/run.js executable
if (process.platform !== "win32") {
  try {
    const binPath = path.join(__dirname, "bin/run.js");
    fs.chmodSync(binPath, "755");
    console.log("✅ Made bin/run.js executable");
  } catch (err) {
    console.log("⚠️  Could not make bin/run.js executable:", err.message);
    process.exit(1);
  }
}

// Test 3: Try to run the install script
console.log("\n🔧 Testing install script...");
try {
  const { execSync } = require("child_process");
  console.log("Executing install script directly...");
  execSync(`node ${path.join(__dirname, "install.js")}`, { stdio: "inherit" });
  console.log("✅ Install script executed successfully");
} catch (err) {
  console.log("❌ Install script failed:", err.message);
  process.exit(1);
}

// Test 4: Create a tarball for testing
console.log("\n📦 Creating test tarball...");
try {
  const { execSync } = require("child_process");
  execSync("npm pack --dry-run", { stdio: "inherit" });
  console.log("✅ Package structure looks good");
} catch (err) {
  console.log("❌ Package creation failed:", err.message);
  process.exit(1);
}

console.log("\n🎯 Local testing complete!");
console.log("\nNext steps:");
console.log(
  "1. If install script failed, make sure the Buildkite repo has releases",
);
console.log(
  "2. Test with: npm pack && npm install -g ./buildkite-buildkite-mcp-server-*.tgz",
);
console.log("3. Then try: buildkite-mcp-server --help");
console.log("4. If everything works, publish with: npm publish");

process.exit(0);
