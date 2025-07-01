#!/usr/bin/env node

const { execSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const https = require('https');
const os = require('os');
const { createGunzip } = require('zlib');
const { pipeline } = require('stream');

const REPO_OWNER = 'buildkite';
const REPO_NAME = 'buildkite-mcp-server';
const BINARY_NAME = 'buildkite-mcp-server';

// Determine platform and architecture
const platform = getPlatform();
const arch = getArch();
const extension = platform === 'windows' ? '.exe' : '';

const binaryPath = path.join(__dirname, 'bin', BINARY_NAME + extension);
const binaryDir = path.dirname(binaryPath);

// Ensure bin directory exists
if (!fs.existsSync(binaryDir)) {
  fs.mkdirSync(binaryDir, { recursive: true });
}

console.log(`Downloading ${BINARY_NAME} for ${platform}-${arch}...`);

// Get the latest release info
const releaseUrl = `https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest`;
const options = {
  headers: {
    'User-Agent': 'nodejs-installer'
  }
};

https.get(releaseUrl, options, (res) => {
  let data = '';
  
  res.on('data', (chunk) => {
    data += chunk;
  });
  
  res.on('end', () => {
    try {
      const releaseInfo = JSON.parse(data);
      const assets = releaseInfo.assets;
      
      if (!assets || assets.length === 0) {
        console.error('No release assets found. The project may not have published binaries yet.');
        console.log('You can build from source using: goreleaser build --snapshot --clean');
        if (require.main === module) {
        process.exit(1);
      } else {
        throw new Error('Installation failed');
      }
      }
       
      const platformName = getPlatformName();
      const archName = getArchName();
      const extension = platformName === 'Windows' ? 'zip' : 'tar.gz';
      
      const possibleNames = [
        `${BINARY_NAME}_${platformName}_${archName}.${extension}`
      ];
      
      let asset = null;
      for (const name of possibleNames) {
        asset = assets.find(a => a.name === name);
        if (asset) break;
      }
      
      if (!asset) {
        console.error(`Could not find binary for ${platform}-${arch}`);
        console.log('Available assets:');
        assets.forEach(a => console.log(`  - ${a.name}`));
        console.log('\nYou can build from source using: goreleaser build --snapshot --clean');
        process.exit(1);
      }
      
      // Download and extract the binary
      // Don't log the full asset URL as it's too noisy
      console.log(`Found release for ${platform}-${arch}`);
      downloadAndExtract(asset.browser_download_url, asset.name, binaryPath);
    } catch (error) {
      console.error('Error parsing release information:', error);
      console.log('The repository may not have releases yet. You can build from source using:');
      console.log('goreleaser build --snapshot --clean');
      process.exit(1);
    }
  });
}).on('error', (err) => {
  console.error('Error fetching release information:', err);
  process.exit(1);
});

function downloadAndExtract(url, fileName, destPath, redirectCount = 0) {
  if (redirectCount > 5) { // Limit redirects to prevent infinite loops
    console.error('Too many redirects, aborting download.');
    process.exit(1);
  }

  const tempFile = path.join(os.tmpdir(), fileName);
  const file = fs.createWriteStream(tempFile);
  
  console.log(`Downloading ${BINARY_NAME} for ${platform}-${arch}...`);
  
  const request = https.get(url, { headers: { 'User-Agent': 'nodejs-installer' } }, (res) => {
    // Handle redirects
    if (res.statusCode === 301 || res.statusCode === 302 || res.statusCode === 307 || res.statusCode === 308) {
      file.close();
      fs.unlink(tempFile, () => {});
      downloadAndExtract(res.headers.location, fileName, destPath, redirectCount + 1);
      return;
    }

    if (res.statusCode !== 200) {
      console.error(`Error downloading binary: Server responded with status code ${res.statusCode} for URL ${url}`);
      file.close();
      fs.unlink(tempFile, () => {}); 
      process.exit(1);
      return;
    }

    // Pipe the response to the file
    res.pipe(file);
    
    // Handle errors on the response stream
    res.on('error', (err) => {
      file.close();
      fs.unlink(tempFile, () => {});
      console.error('Error in response stream:', err);
      process.exit(1);
    });
    
    // Handle file completion
    file.on('finish', () => {
      file.close(() => {
        try {
          const stats = fs.statSync(tempFile);
          console.log(`Downloaded ${BINARY_NAME} (${(stats.size / 1024 / 1024).toFixed(2)} MB)`);

          if (stats.size < 100) { 
            console.error(`Error: Downloaded file ${fileName} is too small (${stats.size} bytes). This might indicate an empty or corrupted archive. Please check the asset on GitHub: ${url}`);
            fs.unlinkSync(tempFile); 
            process.exit(1);
          }

          if (fileName.endsWith('.tar.gz')) {
            extractTarGz(tempFile, path.dirname(destPath), destPath);
          } else if (fileName.endsWith('.zip')) {
            extractZip(tempFile, path.dirname(destPath), destPath);
          } else {
            fs.copyFileSync(tempFile, destPath);
          }
          
          fs.chmodSync(destPath, '755');
          
          // Clean up
          fs.unlinkSync(tempFile);
          
          console.log(`✅ Successfully installed ${BINARY_NAME}`);
          
          process.exit(0);
          
        } catch (error) {
          console.error('Error extracting binary:', error);
          process.exit(1);
        }
      });
    });
  });
  
  // Handle errors on the request
  request.on('error', (err) => {
    file.close();
    fs.unlink(tempFile, () => {});
    console.error('Error downloading binary:', err);
    process.exit(1);
  });
  
  // Set a timeout on the request
  request.setTimeout(60000, () => {
    request.destroy();
    file.close();
    fs.unlink(tempFile, () => {});
    console.error('Download timed out after 60 seconds');
    process.exit(1);
  });
}

function extractTarGz(tarPath, extractDir, finalBinaryPath) {
  try {
    const binaryName = path.basename(finalBinaryPath);
    
    console.log(`Extracting ${BINARY_NAME}...`);
    execSync(`tar -xzf "${tarPath}" -C "${extractDir}"`, { stdio: 'inherit' });
    
    const extractedBinary = findBinary(extractDir, binaryName);
    if (extractedBinary && extractedBinary !== finalBinaryPath) {
      fs.renameSync(extractedBinary, finalBinaryPath);
    } else if (!fs.existsSync(finalBinaryPath)) {
      console.log('Files extracted to bin directory:');
      try {
        const files = fs.readdirSync(extractDir, { recursive: true });
        files.forEach(file => console.log(`  ${file}`));
      } catch (e) {
        console.log('Could not list extracted files');
      }
      throw new Error(`Binary ${binaryName} not found after extraction`);
    }
    
  } catch (error) {
    console.error('Error extracting tar.gz file:', error);
    console.log('Falling back to manual tar parsing...');
    
    // Fallback to the manual method if system tar fails
    extractTarGzManual(tarPath, extractDir, finalBinaryPath);
  }
}

function extractTarGzManual(tarPath, extractDir, finalBinaryPath) {
  const readStream = fs.createReadStream(tarPath);
  const gunzip = createGunzip();
  
  let buffer = Buffer.alloc(0);
  
  pipeline(readStream, gunzip, (err) => {
    if (err) {
      console.error('Error decompressing file:', err);
      process.exit(1);
    }
  });
  
  gunzip.on('data', (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
  });
  
  gunzip.on('end', () => {
    try {
      parseTar(buffer, extractDir, finalBinaryPath);
    } catch (error) {
      console.error('Error parsing tar file:', error);
      console.log('Manual extraction failed. The tar.gz file may have an unsupported format.');
      console.log('Consider installing the tar dependency: npm install tar');
      process.exit(1);
    }
  });
}

function extractZip(zipPath, extractDir, finalBinaryPath) {
  try {
    const binaryName = path.basename(finalBinaryPath);
    
    if (os.platform() === 'win32') {
      // Use PowerShell on Windows
      execSync(`powershell -command "Expand-Archive -Path '${zipPath}' -DestinationPath '${extractDir}' -Force"`, { stdio: 'inherit' });
    } else {
      // Use unzip command on Unix-like systems
      execSync(`unzip -o '${zipPath}' -d '${extractDir}'`, { stdio: 'inherit' });
    }
    
    // Find the extracted binary
    const extractedBinary = findBinary(extractDir, binaryName);
    if (extractedBinary && extractedBinary !== finalBinaryPath) {
      fs.renameSync(extractedBinary, finalBinaryPath);
    } else if (!fs.existsSync(finalBinaryPath)) {
      throw new Error(`Binary ${binaryName} not found after extraction`);
    }
    
  } catch (error) {
    console.error('Error extracting ZIP file:', error);
    console.log('You may need to extract the ZIP file manually and place the binary in the bin directory');
    process.exit(1);
  }
}

function findBinary(dir, binaryName) {
  try {
    const files = fs.readdirSync(dir, { recursive: true });
    for (const file of files) {
      const fullPath = path.join(dir, file);
      if (path.basename(fullPath) === binaryName && fs.statSync(fullPath).isFile()) {
        return fullPath;
      }
    }
  } catch (error) {
    // Fallback for older Node.js versions that don't support recursive option
    return findBinaryRecursive(dir, binaryName);
  }
  return null;
}

function findBinaryRecursive(dir, binaryName) {
  const files = fs.readdirSync(dir);
  for (const file of files) {
    const fullPath = path.join(dir, file);
    const stat = fs.statSync(fullPath);
    
    if (stat.isFile() && path.basename(fullPath) === binaryName) {
      return fullPath;
    } else if (stat.isDirectory()) {
      const found = findBinaryRecursive(fullPath, binaryName);
      if (found) return found;
    }
  }
  return null;
}

function parseTar(buffer, extractDir, finalBinaryPath) {
  // Simple tar parser; we might need to consider using the 'tar' npm package
  let offset = 0;
  const binaryName = path.basename(finalBinaryPath);
  
  while (offset < buffer.length) {
    if (offset + 512 > buffer.length) break;
    
    const header = buffer.slice(offset, offset + 512);
    
    const nameBytes = header.slice(0, 100);
    const name = nameBytes.toString('utf8').replace(/\0.*$/, '');
    
    if (!name) break;
    
    const sizeBytes = header.slice(124, 136);
    const sizeStr = sizeBytes.toString('utf8').replace(/\0.*$/, '').trim();
    const size = parseInt(sizeStr, 8) || 0;
    
    offset += 512; // Skip header
    
    if (name.endsWith(binaryName) || name === binaryName) {
      const fileData = buffer.slice(offset, offset + size);
      fs.writeFileSync(finalBinaryPath, fileData);
      return;
    }
    
    // Skip to next file (round up to 512-byte boundary)
    offset += Math.ceil(size / 512) * 512;
  }
  
  throw new Error(`Binary ${binaryName} not found in tar archive`);
}

function getPlatform() {
  const platform = os.platform();
  
  if (platform === 'darwin') return 'darwin';
  if (platform === 'win32') return 'windows';
  if (platform === 'linux') return 'linux';
  
  throw new Error(`Unsupported platform: ${platform}`);
}

function getPlatformName() {
  const platform = os.platform();
  
  if (platform === 'darwin') return 'Darwin';
  if (platform === 'win32') return 'Windows';
  if (platform === 'linux') return 'Linux';
  
  throw new Error(`Unsupported platform: ${platform}`);
}

function getArch() {
  const arch = os.arch();
  
  if (arch === 'x64') return 'amd64';
  if (arch === 'arm64') return 'arm64';
  if (arch === 'ia32') return '386';
  
  throw new Error(`Unsupported architecture: ${arch}`);
}

function getArchName() {
  const arch = os.arch();
  
  if (arch === 'x64') return 'x86_64';
  if (arch === 'arm64') return 'arm64';
  if (arch === 'ia32') return 'x86';
  
  throw new Error(`Unsupported architecture: ${arch}`);
}