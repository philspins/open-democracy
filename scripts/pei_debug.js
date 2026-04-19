#!/usr/bin/env node
// Submits the Drupal form and captures WDF API calls on the results page.
'use strict';
const puppeteer = require('puppeteer-extra');
const StealthPlugin = require('puppeteer-extra-plugin-stealth');
puppeteer.use(StealthPlugin());

(async () => {
  const browser = await puppeteer.launch({
    executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe',
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox', '--disable-blink-features=AutomationControlled'],
  });
  const page = await browser.newPage();

  // Capture ALL network requests
  await page.setRequestInterception(true);
  page.on('request', req => {
    const url = req.url();
    if (url.includes('/legislative-assembly/') && !url.endsWith('.js') && !url.endsWith('.css')
        && !url.includes('preflight') && !url.includes('permission') && !url.includes('styles')) {
      process.stderr.write('WDF REQ: ' + req.method() + ' ' + url.substring(0, 120) + '\n');
      const pd = req.postData();
      if (pd) process.stderr.write('  BODY: ' + pd + '\n');
    }
    req.continue();
  });

  await page.goto('https://www.assembly.pe.ca/legislative-business/house-records/bills', {
    waitUntil: 'networkidle2',
    timeout: 60000,
  });
  await new Promise(r => setTimeout(r, 3000));

  // Fill year field
  await page.evaluate(() => {
    const yearInput = document.querySelector('#edit-year');
    if (yearInput) {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
      setter.call(yearInput, '2026');
      yearInput.dispatchEvent(new Event('input', { bubbles: true }));
      yearInput.dispatchEvent(new Event('change', { bubbles: true }));
    }
  });

  process.stderr.write('Current URL before submit: ' + page.url() + '\n');

  // Click submit and wait for navigation
  const btn = await page.$('button[type="submit"]');
  if (btn) {
    process.stderr.write('Clicking submit\n');
    await Promise.all([
      page.waitForNavigation({ waitUntil: 'networkidle2', timeout: 30000 }).catch(e => process.stderr.write('nav err: ' + e.message + '\n')),
      btn.click(),
    ]);
  }

  process.stderr.write('URL after submit: ' + page.url() + '\n');

  // Check if gpei-root has different attributes now
  const attrs = await page.evaluate(() => {
    const r = document.querySelector('gpei-root');
    if (!r) return null;
    const a = {};
    for (const attr of r.attributes) a[attr.name] = attr.value;
    return a;
  });
  process.stderr.write('gpei-root attrs: ' + JSON.stringify(attrs) + '\n');

  // Wait for Angular to render results
  await new Promise(r => setTimeout(r, 8000));

  // Check if paginator is rendered now
  const paginatorHTML = await page.evaluate(() => {
    const pg = document.querySelector('gpei-pagination');
    return pg ? pg.outerHTML.substring(0, 500) : 'not found';
  });
  process.stderr.write('Paginator: ' + paginatorHTML + '\n');

  await browser.close();
})().catch(e => {
  process.stderr.write(e.stack + '\n');
  process.exit(1);
});
