import fs from 'node:fs';
import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';

const credentialPath = process.env.WDE_BROWSER_CREDENTIAL_FILE;
if (!credentialPath) {
  throw new Error('WDE_BROWSER_CREDENTIAL_FILE is required');
}

const credential = JSON.parse(fs.readFileSync(credentialPath, 'utf8'));
if (typeof credential.api_key !== 'string' || credential.api_key.length < 32) {
  throw new Error('browser credential file is invalid');
}

async function expectNoAccessibilityViolations(page) {
  const result = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'])
    .analyze();
  expect(result.violations, JSON.stringify(result.violations, null, 2)).toEqual([]);
}

test('login, console responsivo, acessibilidade e logout', async ({ page }, testInfo) => {
  await page.goto('/login');
  await expect(page.getByRole('heading', { name: 'Veja cada tentativa. Entenda cada falha.' })).toBeVisible();
  await expectNoAccessibilityViolations(page);

  await page.getByLabel('API key').fill(credential.api_key);
  await page.getByRole('button', { name: 'Conectar com segurança' }).click();
  await expect(page).toHaveURL(/\/app$/);
  await expect(page.getByRole('heading', { name: 'Visão geral' })).toBeVisible();
  const refreshToggle = page.getByRole('button', { name: 'Pausar atualizações' });
  await expect(refreshToggle).toBeVisible();
  await refreshToggle.click();
  await expect(page.getByRole('button', { name: 'Retomar atualizações' })).toBeVisible();

  const session = (await page.context().cookies()).find((cookie) => cookie.name === 'wde_session');
  expect(session).toBeDefined();
  expect(session.httpOnly).toBe(true);
  expect(session.sameSite).toBe('Strict');

  const hasBodyOverflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(hasBodyOverflow).toBe(false);
  await expectNoAccessibilityViolations(page);

  if (testInfo.project.name.startsWith('mobile')) {
    await expect(page.locator('aside')).toBeHidden();
    await expect(page.getByRole('navigation', { name: 'Navegação principal' })).toBeVisible();
    await testInfo.attach('console-mobile', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
    await page.getByRole('button', { name: 'Sair' }).click();
  } else {
    await expect(page.locator('aside')).toBeVisible();
    await expect(page.locator('.mobile-nav')).toBeHidden();
    await page.getByRole('link', { name: 'Endpoints' }).click();
    await expect(page.getByRole('heading', { name: 'Endpoints' })).toBeVisible();
    await testInfo.attach('console-desktop', { body: await page.screenshot({ fullPage: true }), contentType: 'image/png' });
    await page.getByRole('button', { name: 'Encerrar sessão' }).click();
  }

  await expect(page).toHaveURL(/\/login$/);
  await expect(page.getByRole('button', { name: 'Conectar com segurança' })).toBeVisible();
});
