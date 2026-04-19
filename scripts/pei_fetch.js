#!/usr/bin/env node
// Fetches PEI WDF API data through a real Chrome browser to bypass Radware bot-manager.
// Usage: node scripts/pei_fetch.js <workflowName> <activityName> <jsonQueryVars>
// Prints the WDF API response JSON to stdout, or exits non-zero on failure.

'use strict';
const puppeteer = require('puppeteer-extra');
const StealthPlugin = require('puppeteer-extra-plugin-stealth');
puppeteer.use(StealthPlugin());

const CHROME_PATHS = [
  // Windows
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  // Linux (Ubuntu/Debian)
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium-browser',
  '/usr/bin/chromium',
  // Amazon Linux / RHEL / CentOS
  '/usr/bin/chromium-browser',
  '/usr/bin/google-chrome',
  '/opt/google/chrome/chrome',
  // macOS
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
];

const WDF_BASE = 'https://wdf.princeedwardisland.ca';
const ASSEMBLY_BASE = 'https://www.assembly.pe.ca';

async function main() {
  const args = process.argv.slice(2);
  if (args.length < 3) {
    process.stderr.write('usage: node pei_fetch.js <workflowName> <activityName> <jsonQueryVars>\n');
    process.exit(1);
  }
  const workflowName = args[0];
  const activityName = args[1];
  let queryVars;
  try {
    queryVars = JSON.parse(args[2]);
  } catch (e) {
    process.stderr.write('invalid JSON queryVars: ' + e.message + '\n');
    process.exit(1);
  }

  const executablePath = CHROME_PATHS.find(p => {
    try { require('fs').accessSync(p); return true; } catch { return false; }
  });
  if (!executablePath) {
    process.stderr.write('no Chrome found\n');
    process.exit(1);
  }

  const browser = await puppeteer.launch({
    executablePath,
    headless: true,
    args: [
      '--no-sandbox',
      '--disable-setuid-sandbox',
      '--disable-dev-shm-usage',
      '--disable-blink-features=AutomationControlled',
      '--window-size=1920,1080',
    ],
  });

  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1920, height: 1080 });

    // Navigate to the bills or journals page to seed Radware session cookies.
    const refererPage = workflowName.includes('Journal')
      ? ASSEMBLY_BASE + '/legislative-business/house-records/journals'
      : ASSEMBLY_BASE + '/legislative-business/house-records/bills';

    await page.goto(refererPage, { waitUntil: 'networkidle2', timeout: 45000 }).catch(e => {
      process.stderr.write('warn: goto ' + refererPage + ': ' + e.message + '\n');
    });

    // Make the WDF API call from within the browser context so Radware cookies
    // and fingerprints are included.
    const apiURL = WDF_BASE + '/legislative-assembly/services/api/workflow';
    const body = {
      appName: workflowName,
      featureName: workflowName,
      metaVars: { service_id: null, save_location: null },
      queryVars: Object.assign({ service: workflowName, activity: activityName }, queryVars),
      queryName: activityName,
    };

    const result = await page.evaluate(async (url, bodyObj) => {
      try {
        const resp = await fetch(url, {
          method: 'POST',
          credentials: 'include',
          headers: {
            'Content-Type': 'application/json',
            'Accept': 'application/json',
            'Client-Show-Status': 'true',
          },
          body: JSON.stringify(bodyObj),
        });
        const text = await resp.text();
        return { status: resp.status, body: text };
      } catch (e) {
        return { error: e.message };
      }
    }, apiURL, body);

    if (result.error) {
      process.stderr.write('fetch error: ' + result.error + '\n');
      process.exit(1);
    }
    if (result.status !== 200) {
      process.stderr.write('HTTP ' + result.status + '\n');
      process.exit(1);
    }
    process.stdout.write(result.body);
  } finally {
    await browser.close();
  }
}

main().catch(e => {
  process.stderr.write(e.message + '\n');
  process.exit(1);
});
